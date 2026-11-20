package certgen

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func RunHelper(args []string, input, output, errorOutput *os.File) int {
	fail := func(err error) int { fmt.Fprintln(errorOutput, "Error:", err); return 2 }
	if len(args) == 0 {
		return fail(fmt.Errorf("missing internal command"))
	}
	switch args[0] {
	case "identity":
		if len(args) != 1 {
			return 2
		}
		fmt.Fprintf(output, "%s:%s\n", idText(os.Geteuid()), idText(os.Getegid()))
		return 0
	case "issuance-menu":
		signals := WatchSignals()
		defer signals.Close()
		menu := Menu{Input: input, Output: errorOutput, Context: signals.Context, ANSI: supportsANSI()}
		mode, err := menu.Run()
		if menu.interrupted || signals.Status() != 0 {
			fmt.Fprintln(errorOutput, "Certificate issuance cancelled.")
			if signals.Status() != 0 {
				return signals.Status()
			}
			return 130
		}
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(output, mode)
		return 0
	case "domains":
		issue := len(args) > 1 && args[1] == "--issue"
		index := 1
		if issue {
			index = 2
		}
		if len(args) <= index {
			return 2
		}
		groups, err := LoadDomains(args[index], args[index+1:], issue)
		if err != nil {
			return fail(err)
		}
		for _, group := range groups {
			if group.Count > 100 {
				fmt.Fprintf(output, "%s\t-\t%d\n", group.Name, group.Count)
			} else {
				fmt.Fprintf(output, "%s\t%s\n", group.Name, strings.Join(group.Names, ","))
			}
		}
		return 0
	case "summary":
		report, err := ReadReport(input)
		if err != nil {
			return fail(err)
		}
		if err = WriteSummary(output, report); err != nil {
			return fail(err)
		}
		return 0
	case "resolver-address":
		if len(args) != 3 {
			return 2
		}
		ip := net.ParseIP(args[2])
		if ip == nil || args[1] == "4" && ip.To4() == nil || args[1] == "6" && !strings.Contains(args[2], ":") {
			return 1
		}
		if args[1] != "4" && args[1] != "6" {
			return 2
		}
		return 0
	case "own-output":
		if len(args) != 4 {
			return 2
		}
		uid, err := uintSetting(args[2], 10, 4294967294)
		if err != nil || uid == 0 {
			return fail(fmt.Errorf("invalid UID or GID"))
		}
		gid, err := uintSetting(args[3], 10, 4294967294)
		if err != nil || gid == 0 {
			return fail(fmt.Errorf("invalid UID or GID"))
		}
		if err = os.Chown(args[1], int(uint32(uid)), int(uint32(gid))); err != nil {
			return fail(err)
		}
		return 0
	case "output-path":
		if len(args) != 4 || args[1] != "issue" && args[1] != "dry-run" {
			return 2
		}
		location, err := time.LoadLocation(env("TZ", "Etc/UTC"))
		if err != nil {
			return fail(err)
		}
		path, err := MakeOutputPath(args[1] == "issue", args[2], args[3], location)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(output, path)
		return 0
	case "time-limit":
		if len(args) != 3 {
			return 2
		}
		names, err := strconv.Atoi(args[1])
		if err != nil {
			return fail(err)
		}
		propagation, err := uintSetting(args[2], 10, 2147483647)
		if err != nil {
			return fail(err)
		}
		limit, err := TimeLimit(names, propagation)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(output, limit)
		return 0
	case "settings-probe":
		if len(args) != 2 {
			return 2
		}
		status, err := configureCloudflare(60, args[1], nil)
		if err != nil {
			return fail(err)
		}
		if status == 0 && os.Getenv("CF_DNS_API_TOKEN") == "" && os.Getenv("CF_DNS_API_TOKEN_FILE") == args[1] && os.Getenv("LEGO_DISABLE_CNAME_SUPPORT") == "true" && os.Getenv("CLOUDFLARE_PROPAGATION_TIMEOUT") == "60" {
			return 0
		}
		return 1
	case "supervise":
		signals := WatchSignals()
		defer signals.Close()
		options := ProcessOptions{Output: output, Error: errorOutput, Signals: signals}
		index := 1
		for index+1 < len(args) && strings.HasPrefix(args[index], "--") {
			switch args[index] {
			case "--log":
				options.Log = args[index+1]
			case "--view":
				if args[index+1] != "" {
					options.Names = strings.Split(args[index+1], ",")
				}
			case "--certificate":
				options.Certificate = args[index+1]
			case "--group":
				options.Group = args[index+1]
			default:
				return 2
			}
			index += 2
		}
		if index+1 >= len(args) {
			return 2
		}
		seconds, err := uintSetting(args[index], 12, 86400*365)
		if err != nil {
			return fail(err)
		}
		options.Seconds = seconds
		options.Command = args[index+1:]
		status, err := Supervise(options)
		if err != nil {
			fmt.Fprintln(errorOutput, err)
		}
		return status
	}
	return fail(fmt.Errorf("unknown internal command"))
}
