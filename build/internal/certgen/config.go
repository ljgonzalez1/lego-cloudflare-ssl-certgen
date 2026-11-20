package certgen

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const MaxCertificateNames = 100
const maxConfigurationBytes = 4 << 20
const maxConfigurationNodes = 100000

type DomainGroup struct {
	Name  string
	Names []string
	Count int
}

var groupPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$`)
var decimalName = regexp.MustCompile(`^[0-9]+\.[0-9]*[eE]-[0-9]+$`)
var numericFolder = regexp.MustCompile(`^([1-9][0-9_]*|0[0-7_]*|0x[0-9a-fA-F_]+|0b[01_]+|[0-9]{4}-[0-9]{2}-[0-9]{2})$`)

func validGroup(name string) bool { return groupPattern.MatchString(name) }
func printable(text string) string {
	var b strings.Builder
	for _, ch := range text {
		if ch < 32 || ch == 127 || ch == 27 {
			b.WriteByte('?')
		} else {
			b.WriteRune(ch)
		}
		if b.Len() >= 253 {
			b.WriteString("...")
			break
		}
	}
	if b.Len() == 0 {
		return "(empty)"
	}
	return b.String()
}
func validDomain(name string) bool {
	if len(name) > 253 {
		return false
	}
	name = strings.TrimPrefix(name, "*.")
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	allDigits := true
	for _, ch := range labels[len(labels)-1] {
		if ch < '0' || ch > '9' {
			allDigits = false
		}
	}
	if allDigits {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}
func plainFolderString(name string) bool {
	for _, word := range []string{"yes", "no", "true", "false", "on", "off", "null"} {
		if name == word || name == strings.ToUpper(word) || name == strings.ToUpper(word[:1])+word[1:] {
			return false
		}
	}
	return !numericFolder.MatchString(name)
}

type yamlReader struct {
	anchors map[string]*yaml.Node
	visited map[*yaml.Node]bool
	nodes   int
}

func (reader *yamlReader) inspect(node *yaml.Node) error {
	reader.nodes++
	if reader.nodes > maxConfigurationNodes {
		return fmt.Errorf("YAML configuration exceeds the node limit")
	}
	if node.Anchor != "" {
		if _, found := reader.anchors[node.Anchor]; found {
			return fmt.Errorf("duplicate YAML anchor: %s", printable(node.Anchor))
		}
		reader.anchors[node.Anchor] = node
	}
	for _, child := range node.Content {
		if err := reader.inspect(child); err != nil {
			return err
		}
	}
	return nil
}
func (reader *yamlReader) resolve(node *yaml.Node) (*yaml.Node, error) {
	if node.Kind != yaml.AliasNode {
		return node, nil
	}
	if node.Alias == nil || reader.visited[node.Alias] {
		return nil, fmt.Errorf("undefined or recursive YAML alias: %s", printable(node.Value))
	}
	// Configuration grammar only allows scalar and sequence aliases. A scalar/sequence
	// recursive alias cannot escape the subsequent kind validation.
	return node.Alias, nil
}
func (reader *yamlReader) scalar(node *yaml.Node, diagnostic string) (string, bool, error) {
	resolved, err := reader.resolve(node)
	if err != nil {
		return "", false, err
	}
	node = resolved
	if node.Kind != yaml.ScalarNode {
		return "", false, fmt.Errorf("%s: expected a string", diagnostic)
	}
	explicit := node.Style&yaml.TaggedStyle != 0
	if explicit && node.Tag != "!!str" {
		return "", false, fmt.Errorf("unexpected YAML type: %s", printable(node.Tag))
	}
	implicit := node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0 && !explicit
	return node.Value, implicit, nil
}
func LoadDomains(path string, selected []string, issue bool) ([]DomainGroup, error) {
	// O_NONBLOCK lets us reject named pipes without hanging before fstat.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("could not open configuration: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxConfigurationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxConfigurationBytes {
		return nil, fmt.Errorf("configuration exceeds the 4 MiB limit")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	var document yaml.Node
	if err = decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("expected a nonempty mapping of certificate folders to domain lists")
		}
		if strings.Contains(err.Error(), "unknown anchor") {
			return nil, fmt.Errorf("undefined or recursive YAML alias: %w", err)
		}
		return nil, fmt.Errorf("YAML %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	var extra yaml.Node
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected a single YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode || len(document.Content[0].Content) == 0 {
		return nil, fmt.Errorf("expected a nonempty mapping of certificate folders to domain lists")
	}
	root := document.Content[0]
	if root.Tag != "!!map" {
		return nil, fmt.Errorf("unexpected YAML type: %s", printable(root.Tag))
	}
	reader := yamlReader{anchors: make(map[string]*yaml.Node), visited: make(map[*yaml.Node]bool)}
	if err = reader.inspect(root); err != nil {
		return nil, err
	}
	groups := make([]DomainGroup, 0, len(root.Content)/2)
	known := make(map[string]bool)
	for i := 0; i < len(root.Content); i += 2 {
		name, implicit, err := reader.scalar(root.Content[i], "invalid certificate folder")
		if err != nil {
			return nil, err
		}
		if !validGroup(name) || implicit && !plainFolderString(name) {
			return nil, fmt.Errorf("invalid certificate folder: %s", printable(name))
		}
		if known[name] {
			return nil, fmt.Errorf("duplicate certificate folder: %s", name)
		}
		known[name] = true
		node, err := reader.resolve(root.Content[i+1])
		if err != nil {
			return nil, err
		}
		if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
			return nil, fmt.Errorf("%s: expected a nonempty domain list", name)
		}
		if node.Tag != "!!seq" {
			return nil, fmt.Errorf("unexpected YAML type: %s", printable(node.Tag))
		}
		reader.visited[node] = true
		group := DomainGroup{Name: name, Count: len(node.Content)}
		seen := make(map[string]bool, len(node.Content))
		for _, item := range node.Content {
			value, implicit, err := reader.scalar(item, name+": invalid DNS name")
			if err != nil {
				return nil, err
			}
			value = strings.TrimSuffix(value, ".")
			if !validDomain(value) || implicit && decimalName.MatchString(value) {
				return nil, fmt.Errorf("%s: invalid DNS name: %s", name, printable(value))
			}
			value = strings.ToLower(value)
			if seen[value] {
				return nil, fmt.Errorf("%s: duplicate DNS name: %s", name, value)
			}
			seen[value] = true
			group.Names = append(group.Names, value)
		}
		delete(reader.visited, node)
		if !issue && group.Count > MaxCertificateNames {
			return nil, fmt.Errorf("%s: identifiers exceed the 100-name limit", name)
		}
		groups = append(groups, group)
	}
	wanted := make(map[string]bool, len(selected))
	missing := []string{}
	for _, name := range selected {
		if !known[name] && !wanted[name] {
			missing = append(missing, printable(name))
		}
		wanted[name] = true
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown certificate folder(s): %s", strings.Join(missing, ", "))
	}
	if len(wanted) == 0 {
		return groups, nil
	}
	result := make([]DomainGroup, 0, len(wanted))
	for _, group := range groups {
		if wanted[group.Name] {
			result = append(result, group)
		}
	}
	return result, nil
}
