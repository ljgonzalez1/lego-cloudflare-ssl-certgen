package certgen

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const Usage = `Usage: certgen [--list | --dry-run | --test | --staging | --production]
               [--only FOLDER]... [--config FILE] [--output DIRECTORY]

  --list          Validate the YAML and list certificate groups
  --dry-run       Show the planned groups, domains and output paths
  --test          Check lego and preview an offline staging run for CI
  --staging       Issue staging certificates without prompting
  --production    Issue live certificates without confirmation
  --only FOLDER   Process only this group (repeatable)
  --config FILE   Read this YAML file (default: /app/domains.yaml)
  --output DIR    Write to this directory (default: /ssl-certs)
`

type Options struct {
	Action, Mode, Config, Output string
	Groups                       []string
	Help                         bool
}

func ParseOptions(arguments []string) (Options, error) {
	options := Options{Action: "issue", Config: "/app/domains.yaml", Output: "/ssl-certs"}
	for i := 0; i < len(arguments); i++ {
		option := arguments[i]
		switch option {
		case "--help", "-h":
			options.Help = true
			return options, nil
		case "--list", "--dry-run", "--test":
			action := strings.TrimPrefix(option, "--")
			if options.Action != "issue" && options.Action != action {
				return options, errors.New("--list, --dry-run and --test are mutually exclusive.")
			}
			options.Action = action
		case "--staging", "--production":
			mode := strings.TrimPrefix(option, "--")
			if options.Mode != "" && options.Mode != mode {
				return options, errors.New("--staging and --production are mutually exclusive.")
			}
			options.Mode = mode
		case "--only", "--config", "--output":
			if i+1 >= len(arguments) || arguments[i+1] == "" || strings.HasPrefix(arguments[i+1], "--") {
				return options, fmt.Errorf("%s requires a value.", option)
			}
			i++
			value := arguments[i]
			switch option {
			case "--only":
				options.Groups = append(options.Groups, value)
			case "--config":
				options.Config = value
			case "--output":
				options.Output = value
			}
		default:
			return options, fmt.Errorf("Unknown argument: %s", printable(option))
		}
	}
	if options.Action != "issue" && options.Mode != "" {
		return options, errors.New("--staging and --production cannot be combined with --list, --dry-run or --test.")
	}
	return options, nil
}

type App struct {
	Input, Output, Error *os.File
	Signals              *SignalState
	Redactor             *Redactor
}

func (app *App) message(output *os.File, icon, text, style string) {
	fmt.Fprintln(output, paint(icon+text, style, isTerminal(output) && supportsANSI()))
}
func (app *App) info(text string) { app.message(app.Output, "ℹ️  ", text, "36") }
func (app *App) fail(err error) {
	if err != nil {
		app.message(app.Error, "❌ ", app.Redactor.Safe(err.Error()), "31")
	}
}
func (app *App) section(title string) {
	fmt.Fprintln(app.Output)
	app.message(app.Output, "", title, "1;35")
}
func (app *App) Run(arguments []string) int {
	options, err := ParseOptions(arguments)
	if err != nil {
		app.fail(err)
		if strings.HasPrefix(err.Error(), "Unknown argument") {
			fmt.Fprint(app.Error, Usage)
		}
		return 2
	}
	if options.Help {
		fmt.Fprint(app.Output, Usage)
		return 0
	}
	identity, err := ReadIdentity()
	if err != nil {
		app.fail(err)
		return 2
	}
	root := os.Geteuid() == 0
	if !root && (os.Geteuid() != identity.UID || os.Getegid() != identity.GID) {
		app.fail(fmt.Errorf("The container user must match the configured UID/GID (%s:%s).", idText(identity.UID), idText(identity.GID)))
		return 1
	}
	issue := options.Action == "issue"
	if issue && options.Mode == "" {
		menu := Menu{Input: app.Input, Output: app.Error, Context: app.Signals.Context, ANSI: supportsANSI()}
		options.Mode, err = menu.Run()
		if menu.interrupted || app.Signals.Status() != 0 {
			fmt.Fprintln(app.Error, "Certificate issuance cancelled.")
			if app.Signals.Status() != 0 {
				return app.Signals.Status()
			}
			return 130
		}
		if err != nil {
			app.fail(err)
			return 2
		}
		if options.Mode == "cancel" {
			app.info("Certificate issuance cancelled.")
			return 0
		}
	} else if issue && options.Mode == "production" {
		app.message(app.Error, "⚠️  ", "Production requested by --production. This run submits live orders without confirmation.", "33")
	}
	if options.Mode == "" {
		options.Mode = "staging"
	}
	if root {
		if issue && options.Output == "/ssl-certs" {
			if !outputMounted(options.Output) {
				app.message(app.Output, "⚠️  ", "Mount a host directory at /ssl-certs to keep the output.", "33")
			}
			if err = os.Chown(options.Output, identity.UID, identity.GID); err != nil {
				app.fail(fmt.Errorf("Cannot set output ownership: %w", err))
				return 1
			}
		}
		if err = dropPrivileges(identity); err != nil {
			app.fail(err)
			return 1
		}
	}
	// Apply before creating any certificate, account or report files.
	syscall.Umask(0077)
	groups, err := LoadDomains(options.Config, options.Groups, issue)
	if err != nil {
		app.fail(fmt.Errorf("Error in %s: %w", printable(options.Config), err))
		return 2
	}
	if options.Action == "list" {
		for _, group := range groups {
			fmt.Fprintf(app.Output, "%s: %d domains\n", group.Name, group.Count)
		}
		return 0
	}
	settings, err := ReadSettings(issue, app.Output)
	setupStatus := 0
	setupResult := 2
	if err != nil {
		app.fail(err)
		setupStatus = 2
	} else if issue {
		credentialStatus, credentialErr := configureCloudflare(settings.Propagation, "/run/secrets/cloudflare_api_token", app.Redactor)
		if credentialStatus != 0 {
			app.fail(credentialErr)
			setupStatus = credentialStatus
			if credentialStatus == 3 {
				setupStatus = 2
				setupResult = 1
			}
		} else {
			info, pathErr := os.Stat(options.Output)
			if pathErr != nil || !info.IsDir() || syscall.Access(options.Output, 3) != nil {
				app.fail(fmt.Errorf("Output directory must be writable by UID %s and GID %s: %s", idText(identity.UID), idText(identity.GID), options.Output))
				setupStatus = 1
			} else if _, err = exec.LookPath("lego"); err != nil {
				app.fail(errors.New("lego is missing. Rebuild the image."))
				setupStatus = 1
			}
		}
	}
	if options.Action == "test" {
		if setupStatus != 0 {
			return setupStatus
		}
		capture := LimitedCapture{Limit: 1024}
		status, err := Supervise(ProcessOptions{Command: []string{"lego", "--version"}, Seconds: 10, Output: app.Output, Error: app.Error, Capture: &capture, Signals: app.Signals, Redactor: app.Redactor})
		if app.Signals.Status() != 0 {
			return app.Signals.Status()
		}
		if err != nil || status != 0 {
			app.fail(fmt.Errorf("Could not check the lego executable (exit %d).", status))
			return 1
		}
		version := strings.TrimRight(capture.String(), "\n")
		if !strings.HasPrefix(version, "lego version ") {
			app.fail(errors.New("Unexpected response from lego --version."))
			return 1
		}
		app.section("🧪 TEST MODE (offline)")
		app.info(version)
	}
	server := StagingServer
	if options.Mode == "production" {
		server = ProductionServer
	}
	if setupStatus == 0 {
		if options.Mode == "production" {
			app.section("🚨 PRODUCTION MODE")
		} else {
			app.section("🧪 STAGING MODE")
		}
		app.info("ACME server: " + server)
		app.info("Cloudflare DNS-01 validates each name with a temporary TXT record.")
		app.info("Timezone: " + settings.Timezone)
		app.info("Target UID: " + idText(identity.UID) + "  GID: " + idText(identity.GID))
	}
	report := make([]ReportRow, 0, len(groups))
	exitStatus := setupStatus
	for _, group := range groups {
		if app.Signals.Status() != 0 {
			return app.Signals.Status()
		}
		result := setupResult
		if group.Count > MaxCertificateNames {
			result = 1
			app.fail(fmt.Errorf("%s: %d DNS names exceed the 100-name limit. Split them into smaller certificate groups.", group.Name, group.Count))
		} else if setupStatus == 0 {
			result = app.runGroup(group, options, settings, server)
		}
		if result != 0 && exitStatus == 0 {
			exitStatus = 1
		}
		report = append(report, ReportRow{group.Name, group.Count, result})
	}
	if app.Signals.Status() != 0 {
		return app.Signals.Status()
	}
	if issue {
		app.section("📋 Certificate request summary (DNS names per certificate)")
		if err = WriteSummary(app.Output, report); err != nil {
			app.fail(err)
			return 1
		}
	}
	if options.Action == "test" && exitStatus == 0 {
		app.message(app.Output, "✅ ", "Offline test passed.", "32")
	}
	return exitStatus
}
func (app *App) runGroup(group DomainGroup, options Options, settings Settings, server string) int {
	issue := options.Action == "issue"
	target, err := MakeOutputPath(issue, options.Output, group.Name, settings.Location)
	if err != nil {
		app.fail(err)
		return 2
	}
	limit, err := TimeLimit(group.Count, settings.Propagation)
	if err != nil {
		app.fail(err)
		return 2
	}
	app.section("🚀 " + group.Name)
	app.info("Output path: " + target)
	app.info(fmt.Sprintf("Time limit: %ds (%d min)", limit, (limit+59)/60))
	app.info(fmt.Sprintf("Domains (%d):", group.Count))
	if !issue || !settings.Pretty {
		for _, name := range group.Names {
			app.message(app.Output, "🔹 ", name, "34")
		}
	}
	if !issue {
		return 0
	}
	if app.Signals.Status() != 0 {
		return 1
	}
	first := group.Names[0]
	if strings.HasPrefix(first, "*.") {
		first = "_" + first[1:]
	}
	command := []string{"lego", "--accept-tos", "--email", settings.Email, "--dns", "cloudflare", "--server", server, "--path", target}
	for _, resolver := range settings.Resolvers {
		command = append(command, "--dns.resolvers", resolver)
	}
	for _, name := range group.Names {
		command = append(command, "--domains", name)
	}
	command = append(command, "run")
	var names []string
	if settings.Pretty {
		names = group.Names
	}
	status, err := Supervise(ProcessOptions{Command: command, Seconds: limit, Certificate: target + "/certificates/" + first, Group: group.Name, Names: names, Output: app.Output, Error: app.Error, Signals: app.Signals, Redactor: app.Redactor})
	if app.Signals.Status() != 0 {
		app.message(app.Error, "⚠️  ", "Certificate issuance cancelled.", "33")
		return 1
	}
	if err != nil {
		app.fail(err)
		return 1
	}
	if status == 0 {
		app.message(app.Output, "✅ ", "Certificates: "+target+"/certificates/", "32")
		app.message(app.Output, "✅ ", "Account data: "+target+"/accounts/", "32")
		return 0
	}
	return 1
}
