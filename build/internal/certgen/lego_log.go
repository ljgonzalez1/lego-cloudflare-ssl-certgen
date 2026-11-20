package certgen

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var timestampPattern = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} `)

type LegoLine struct {
	Timestamp, Message string
	Level              string
}

func parseLegoLine(text string) LegoLine {
	line := LegoLine{Message: text, Level: "text"}
	if timestampPattern.MatchString(text) {
		line.Timestamp = text[:19]
		line.Message = text[20:]
	}
	line.Message = strings.TrimLeft(line.Message, " \t\r")
	for _, level := range []struct{ prefix, name string }{{"[INFO] ", "info"}, {"[WARN] ", "warning"}} {
		if strings.HasPrefix(line.Message, level.prefix) {
			line.Message = strings.TrimPrefix(line.Message, level.prefix)
			line.Level = level.name
			break
		}
	}
	return line
}
func shortLog(text string) string {
	text = strings.Join(strings.FieldsFunc(text, func(ch rune) bool { return unicode.IsControl(ch) || unicode.IsSpace(ch) }), " ")
	if len(text) > 220 {
		end := 220
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end] + "..."
	}
	return text
}
func (line LegoLine) fatalHeader() bool {
	if line.Level != "text" {
		return false
	}
	for _, prefix := range []string{"Could not obtain certificates:", "Could not complete registration", "Could not create client:", "Could not load file for account ", "Could not parse file for account ", "Could not load account for ", "Could not save account for ", "Could not check/create", "Unable to save ", "cloudflare: some credentials information are missing", "Unable to marshal CertResource ", "Server requires External Account Binding.", "You did not accept the TOS.", "panic: ", "fatal error: "} {
		if strings.HasPrefix(line.Message, prefix) {
			return true
		}
	}
	return false
}
func removeNamePrefixes(text string) (string, []string) {
	var names []string
	for strings.HasPrefix(text, "[") {
		end := strings.Index(text, "] ")
		if end < 0 {
			break
		}
		names = append(names, text[1:end])
		text = text[end+2:]
	}
	return text, names
}
func (line LegoLine) domainFailure() bool {
	detail, _ := removeNamePrefixes(line.Message)
	return line.Level == "text" && strings.HasPrefix(detail, "acme: error presenting token:")
}

const (
	causeNone = iota
	causeFatal
	causePropagation
	causeRejected
	causeZone
	causeAccess
	causeToken
	causeNameLimit
	causeRateLimit
)
const rateLimitMarker = " :: urn:ietf:params:acme:error:rateLimited :: "

type LegoLog struct {
	Cause                       int
	Header, Detail, LastMessage string
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
func (log *LegoLog) record(message string) {
	if message == "" || strings.HasPrefix(message, "error: one or more domains had a problem:") {
		return
	}
	cause := causeFatal
	switch {
	case strings.Contains(message, rateLimitMarker):
		cause = causeRateLimit
		message = strings.SplitN(message, rateLimitMarker, 2)[1]
	case containsAny(message, ":tooManyIdentifiers", "too many identifiers", "too many domains"):
		cause = causeNameLimit
	case strings.Contains(message, "cloudflare: "):
		lower := strings.ToLower(message)
		switch {
		case containsAny(lower, "invalid access token", "invalid api token", "invalid format for authorization header", "9109:", "6111:", "[status code 401]"):
			cause = causeToken
		case containsAny(lower, "authentication error", "[status code 403]", "10000:"):
			cause = causeAccess
		case containsAny(lower, "failed to find zone ", "could not find zone for domain"):
			cause = causeZone
		}
	case strings.Contains(message, "propagation: time limit exceeded"):
		cause = causePropagation
	case containsAny(message, "invalid authorization", "invalid challenge", ":unauthorized :: ", ":rejectedIdentifier :: ", ":caa :: "):
		cause = causeRejected
	}
	if cause > log.Cause || cause == log.Cause && log.Detail == "" {
		log.Cause = cause
		log.Detail = shortLog(message)
	}
}
func (log *LegoLog) Observe(line LegoLine) {
	switch {
	case strings.HasPrefix(line.Message, "retry: acme: error: ") && strings.Contains(line.Message, rateLimitMarker):
		log.record(line.Message)
	case line.fatalHeader() || line.domainFailure():
		if line.fatalHeader() && log.Header == "" {
			log.Header = shortLog(line.Message)
		}
		log.record(line.Message)
		if log.Detail == log.Header {
			log.Detail = ""
		}
	case log.Header != "" && line.Level == "text" && line.Timestamp == "":
		log.record(line.Message)
	case line.Level == "text" && (line.Timestamp != "" || strings.HasPrefix(line.Message, "[ERROR] ")):
		log.LastMessage = shortLog(line.Message)
	}
}
func (log *LegoLog) Diagnostic(status int) string {
	switch log.Cause {
	case causeRateLimit:
		if log.Detail == "" {
			return "Let's Encrypt rate limit reached. Wait before retrying."
		}
		return "Let's Encrypt rate limit reached. ACME response: " + log.Detail
	case causeNameLimit:
		return "Let's Encrypt rejected the number of DNS names. Use at most 100 per certificate."
	case causeToken:
		return "Cloudflare rejected the API token. Check that it is valid and active."
	case causeAccess:
		return "Cloudflare denied API access. Check the token, Zone Read, DNS Edit and the zone scope."
	case causeZone:
		return "Cloudflare zone lookup failed. Check DNS delegation and the token's Zone Read and zone scope."
	case causePropagation:
		return "DNS propagation timed out. Check the challenge TXT records and DNS resolvers."
	case causeRejected:
		return "Let's Encrypt rejected the order. " + log.Detail
	case causeFatal:
		if log.Header == "" {
			return log.Detail
		}
		if log.Detail == "" {
			return log.Header
		}
		return log.Header + " " + log.Detail
	}
	message := fmt.Sprintf("lego exited with code %d.", status)
	if log.LastMessage != "" {
		message += " " + log.LastMessage
	}
	return message
}
