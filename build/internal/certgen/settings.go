package certgen

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const StagingServer = "https://acme-staging-v02.api.letsencrypt.org/directory"
const ProductionServer = "https://acme-v02.api.letsencrypt.org/directory"

type Identity struct{ UID, GID int }
type Settings struct {
	Timezone, Email string
	Location        *time.Location
	Propagation     uint64
	Pretty          bool
	Resolvers       []string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func uintSetting(text string, digits int, maximum uint64) (uint64, error) {
	if len(text) == 0 || len(text) > digits {
		return 0, errors.New("expected an unsigned integer")
	}
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return 0, errors.New("expected an unsigned integer")
		}
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil || value > maximum {
		return 0, errors.New("expected an unsigned integer in range")
	}
	return value, nil
}
func ReadIdentity() (Identity, error) {
	var identity Identity
	for _, field := range []struct {
		key, legacy string
		value       *int
	}{{"CERT_UID", "UID", &identity.UID}, {"CERT_GID", "GID", &identity.GID}} {
		value, err := uintSetting(env(field.key, env(field.legacy, "1000")), 10, 4294967294)
		if err != nil || value == 0 {
			return identity, fmt.Errorf("%s must be an integer from 1 to 4294967294.", field.legacy)
		}
		// int is 32 bits on ARMv6/ARMv7/i386. Setuid takes the raw 32-bit uid_t;
		// Go's syscall API represents it as int and preserves that bit pattern.
		*field.value = int(uint32(value))
	}
	return identity, nil
}
func idText(value int) string { return strconv.FormatUint(uint64(uint32(value)), 10) }
func validEmail(email string) bool {
	if strings.Count(email, "@") != 1 {
		return false
	}
	parts := strings.SplitN(email, "@", 2)
	if parts[0] == "" {
		return false
	}
	for _, ch := range email {
		if ch <= 32 || ch == 127 {
			return false
		}
	}
	host := parts[1]
	if !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.HasPrefix(host, "-") || strings.HasSuffix(host, "-") || strings.Contains(host, "..") || strings.Contains(host, ".-") || strings.Contains(host, "-.") {
		return false
	}
	for _, ch := range host {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-') {
			return false
		}
	}
	return true
}
func validResolver(value string) bool {
	at := strings.LastIndexByte(value, ':')
	if at < 1 {
		return false
	}
	host := value[:at]
	port, err := uintSetting(value[at+1:], 5, 65535)
	if err != nil || port == 0 {
		return false
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		ip := net.ParseIP(host[1 : len(host)-1])
		return ip != nil && strings.Contains(host, ":")
	}
	numeric := true
	for _, ch := range host {
		if !(ch >= '0' && ch <= '9' || ch == '.') {
			numeric = false
		}
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
			return false
		}
	}
	return !(numeric && strings.Contains(host, ".") && net.ParseIP(host) == nil)
}
func ReadSettings(issue bool, stdout *os.File) (Settings, error) {
	result := Settings{Timezone: env("TZ", "Etc/UTC"), Email: os.Getenv("EMAIL")}
	var problems []string
	var err error
	if result.Timezone == "Local" || strings.HasPrefix(result.Timezone, "/") || strings.Contains(result.Timezone, "..") {
		problems = append(problems, "TZ: expected an IANA timezone such as Etc/UTC or America/Santiago.")
	} else {
		result.Location, err = time.LoadLocation(result.Timezone)
		if err != nil {
			problems = append(problems, "TZ: expected an IANA timezone such as Etc/UTC or America/Santiago.")
		}
	}
	if (issue || result.Email != "") && !validEmail(result.Email) {
		problems = append(problems, "EMAIL: enter a valid Let's Encrypt contact address.")
	}
	if issue && !strings.EqualFold(os.Getenv("ACCEPT_LEGO_TOS"), "true") {
		problems = append(problems, "ACCEPT_LEGO_TOS: set to true after reviewing https://letsencrypt.org/repository/")
	}
	result.Propagation, err = uintSetting(env("PROPAGATION_SECONDS", "360"), 10, 2147483647)
	if err != nil {
		problems = append(problems, "PROPAGATION_SECONDS: expected a non-negative integer in seconds.")
	}
	output := env("LEGO_OUTPUT", "auto")
	if output != "auto" && output != "pretty" && output != "raw" {
		problems = append(problems, "LEGO_OUTPUT: expected auto, pretty or raw.")
	}
	result.Pretty = output == "pretty" || output == "auto" && isTerminal(stdout)
	for _, resolver := range strings.Split(env("DNS_RESOLVERS", "1.1.1.1:53,8.8.8.8:53,1.0.0.1:53"), ",") {
		resolver = strings.TrimSpace(resolver)
		if !validResolver(resolver) {
			problems = append(problems, "DNS_RESOLVERS: expected comma-separated host:port entries (use brackets for IPv6).")
			break
		}
		result.Resolvers = append(result.Resolvers, resolver)
	}
	if len(problems) > 0 {
		return result, errors.New("Environment validation failed.\n  " + strings.Join(problems, "\n  "))
	}
	return result, nil
}
func configureCloudflare(propagation uint64, secret string, diagnostics *Diagnostics) (int, error) {
	setting := strings.ToLower(env("LEGO_DISABLE_CNAME_SUPPORT", "true"))
	disable := "true"
	switch setting {
	case "true", "t", "1":
	case "false", "f", "0":
		disable = "false"
	default:
		return 2, errors.New("LEGO_DISABLE_CNAME_SUPPORT must be true or false.")
	}
	token := env("CLOUDFLARE_API_KEY", env("CF_DNS_API_TOKEN", os.Getenv("CLOUDFLARE_API_TOKEN")))
	useFile := false
	if token == "" {
		info, err := os.Stat(secret)
		if err == nil && info.Mode().IsRegular() {
			file, err := os.OpenFile(secret, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return 2, errors.New("Could not read the Cloudflare API token file.")
			}
			contents, err := io.ReadAll(io.LimitReader(file, 4097))
			file.Close()
			if err != nil || len(contents) > 4096 {
				return 2, errors.New("Could not read the Cloudflare API token file.")
			}
			token = strings.TrimRight(string(contents), "\n")
			useFile = true
		}
	}
	if diagnostics != nil {
		diagnostics.AddSecret(token)
	}
	valid := token != ""
	for _, ch := range token {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			valid = false
		}
	}
	if !valid {
		return 3, errors.New("Provide a valid Cloudflare DNS API token through the Compose secret or CLOUDFLARE_API_TOKEN.")
	}
	for _, name := range []string{"CLOUDFLARE_API_KEY", "CLOUDFLARE_API_KEY_FILE", "CF_API_KEY", "CF_API_KEY_FILE", "CLOUDFLARE_API_TOKEN", "CF_DNS_API_TOKEN", "CF_DNS_API_TOKEN_FILE", "CLOUDFLARE_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN_FILE"} {
		if err := os.Unsetenv(name); err != nil {
			return 2, err
		}
	}
	values := map[string]string{"CLOUDFLARE_PROPAGATION_TIMEOUT": strconv.FormatUint(propagation, 10), "LEGO_DISABLE_CNAME_SUPPORT": disable}
	if useFile {
		values["CF_DNS_API_TOKEN_FILE"] = filepath.Clean(secret)
	} else {
		values["CF_DNS_API_TOKEN"] = token
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			return 2, errors.New("Cannot configure the Cloudflare environment.")
		}
	}
	return 0, nil
}
