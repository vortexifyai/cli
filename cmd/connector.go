package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"vortexify-cli/internal/api"
)

var errConfigurationCancelled = errors.New("configuration cancelled")

type configurationWizard struct {
	root map[string]any
	out  io.Writer
	read func(secret bool) (string, error)
}

func newConnectorCommand(opts *options) *cobra.Command {
	var configure, check, discover, wait, asJSON bool
	var version int
	var timeout time.Duration
	command := &cobra.Command{Use: "connector <name> --configure|--check|--discover", Short: "Configure a connector, check its connection, or discover source tables", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !configure && !check && !discover {
			return fmt.Errorf("choose --configure, --check, or --discover")
		}
		if configure {
			for _, flag := range []string{"version", "wait", "timeout", "json"} {
				if cmd.Flags().Changed(flag) {
					return fmt.Errorf("--%s is only available with --check or --discover", flag)
				}
			}
		} else if version < 0 || cmd.Flags().Changed("version") && version == 0 {
			return fmt.Errorf("version must be positive")
		}
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		rows, err := client.GetResourceGraph(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		id := ""
		for _, row := range rows {
			if row.Type == "connector" && (row.ID == args[0] || row.Name == args[0]) {
				if id != "" {
					return fmt.Errorf("ambiguous connector; use its ID")
				}
				id = row.ID
			}
		}
		if id == "" {
			return fmt.Errorf("connector is not registered; create and commit it first")
		}
		if check || discover {
			action := "check"
			if discover {
				action = "discover"
			}
			result, err := client.StartConnectorOperation(cmd.Context(), cfg.ProjectID, id, action, version)
			if err != nil {
				return err
			}
			jobID, _ := result["job_id"].(string)
			if jobID == "" {
				jobID, _ = result["id"].(string)
			}
			if jobID == "" {
				return fmt.Errorf("server returned an empty connector job ID")
			}
			result["job_id"] = jobID
			if wait {
				result, err = waitForJobDisplay(cmd, client, cfg.ProjectID, jobID, timeout, asJSON)
				if err != nil {
					return err
				}
			}
			result["resource_name"] = args[0]
			if result["type"] == nil {
				result["type"] = "connector-check"
				if discover {
					result["type"] = "connector-discovery"
				}
			}
			if err := printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
				return err
			}
			return executionFailure(result)
		}
		current, err := client.GetConnectorConfiguration(cmd.Context(), cfg.ProjectID, id)
		if err != nil {
			return err
		}
		if current.Schema == nil {
			return fmt.Errorf("commit the connector's config.json schema before configuring it")
		}
		if current.Schema == false {
			return fmt.Errorf("the committed schema does not allow configuration")
		}
		schema, _ := current.Schema.(map[string]any)
		if schema == nil {
			schema = map[string]any{}
		}
		wizard := configurationWizard{root: schema, out: cmd.OutOrStdout(), read: configurationReader(cmd)}
		values, err := wizard.run(current.Configuration)
		if errors.Is(err, errConfigurationCancelled) {
			fmt.Fprintln(cmd.OutOrStdout(), "Configuration cancelled; nothing saved.")
			return nil
		}
		if err != nil {
			return err
		}
		// The server validates the full JSON Schema and protects secret fields.
		saved, err := client.ConfigureConnector(cmd.Context(), cfg.ProjectID, id, values)
		if err != nil {
			var response *api.APIError
			if errors.As(err, &response) {
				return fmt.Errorf("configuration was not saved (HTTP %d); check access and values against the committed schema", response.StatusCode)
			}
			return fmt.Errorf("configuration was not saved; check the server connection and retry")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Configured %s (version %d). Deployment unchanged.\n", args[0], saved.Version)
		return nil
	}}
	command.Flags().BoolVar(&configure, "configure", false, "Prompt for server configuration values; secret input is hidden")
	command.Flags().BoolVar(&check, "check", false, "Check connectivity using saved source and configuration")
	command.Flags().BoolVar(&discover, "discover", false, "Discover source tables and column schemas using saved configuration")
	command.MarkFlagsMutuallyExclusive("configure", "check", "discover")
	command.Flags().IntVar(&version, "version", 0, "Saved connector version for check/discovery (defaults to latest)")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for check/discovery results")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum wait duration; does not cancel the job")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured check/discovery results")
	return command
}

// Read one byte at a time for ordinary prompts so buffered input never consumes
// a later secret line before ReadPassword disables terminal echo.
func configurationReader(cmd *cobra.Command) func(bool) (string, error) {
	return func(secret bool) (string, error) {
		type result struct {
			value string
			err   error
		}
		input := cmd.InOrStdin()
		var restore func()
		var fd int
		if secret {
			file, ok := input.(*os.File)
			if !ok || !term.IsTerminal(int(file.Fd())) {
				return "", fmt.Errorf("secret entry requires an interactive terminal")
			}
			fd = int(file.Fd())
			state, err := term.GetState(fd)
			if err != nil {
				return "", err
			}
			restore = func() { _ = term.Restore(fd, state) }
		}
		done := make(chan result, 1)
		go func() {
			if secret {
				value, err := term.ReadPassword(fd)
				done <- result{string(value), err}
				return
			}
			var value strings.Builder
			var b [1]byte
			for {
				n, err := input.Read(b[:])
				if n > 0 {
					if b[0] == '\n' {
						done <- result{strings.TrimSuffix(value.String(), "\r"), nil}
						return
					}
					value.WriteByte(b[0])
				}
				if err != nil {
					done <- result{value.String(), err}
					return
				}
			}
		}()
		select {
		case <-cmd.Context().Done():
			if restore != nil {
				restore()
			}
			return "", cmd.Context().Err()
		case result := <-done:
			if secret {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return result.value, result.err
		}
	}
}

func (w configurationWizard) run(previous map[string]any) (map[string]any, error) {
	root, err := w.schema(w.root, map[string]bool{})
	if err != nil {
		return nil, err
	}
	properties, _ := root["properties"].(map[string]any)
	if root["type"] == nil {
		root["type"] = "object"
	}
	required := map[string]bool{}
	for _, key := range configArray(root["required"]) {
		if name, ok := key.(string); ok {
			required[name] = true
		}
	}
	values := maps.Clone(previous)
	if values == nil {
		values = map[string]any{}
	}
	fmt.Fprintln(w.out, "Enter keeps a saved value, accepts a default, or skips an optional field. :clear removes an optional value; :empty sets an empty string; :cancel exits without saving. Objects and arrays use JSON.")
	names := slices.Sorted(maps.Keys(properties))
	slices.SortStableFunc(names, func(a, b string) int {
		left, _ := properties[a].(map[string]any)
		right, _ := properties[b].(map[string]any)
		x, _ := configurationNumber(left["order"])
		y, _ := configurationNumber(right["order"])
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return strings.Compare(a, b)
	})
	// Complex/root-secret schemas are entered as one JSON object so secret
	// annotations on conditional branches are never missed by individual prompts.
	for _, keyword := range []string{"allOf", "oneOf", "anyOf", "if", "dependentSchemas", "patternProperties"} {
		if root[keyword] != nil {
			names = nil
		}
	}
	if root["x-vortexify-secret"] == true {
		names = nil
	}
	// Open or composed schemas can be supplied as JSON.
	if len(names) == 0 {
		value, keep, err := w.field("configuration", root, values, true, true)
		if err != nil {
			return nil, err
		}
		if !keep {
			var ok bool
			values, ok = value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("configuration must be a JSON object")
			}
		}
	}
	for _, name := range names {
		schema, err := w.schema(properties[name], map[string]bool{})
		if err != nil {
			return nil, err
		}
		old, exists := values[name]
		value, keep, err := w.field(name, schema, old, exists, required[name])
		if err != nil {
			return nil, err
		}
		if keep {
			if !exists {
				delete(values, name)
			}
			continue
		}
		if value == clearConfigurationValue {
			delete(values, name)
		} else {
			values[name] = value
		}
	}
	fmt.Fprintln(w.out, "\nConfiguration to save:")
	for _, name := range slices.Sorted(maps.Keys(values)) {
		schema, _ := w.schema(properties[name], map[string]bool{})
		if len(names) == 0 {
			schema = root
		}
		display := "[hidden]"
		if !w.sensitive(schema, map[string]bool{}) && !hasConfigurationSecret(values[name]) {
			encoded, _ := json.Marshal(values[name])
			display = string(encoded)
		}
		fmt.Fprintf(w.out, "  %s: %s\n", name, display)
	}
	fmt.Fprint(w.out, "Save configuration? [y/N]: ")
	answer, err := w.read(false)
	if err != nil {
		return nil, err
	}
	if answer != "y" && answer != "Y" && !strings.EqualFold(answer, "yes") {
		return nil, errConfigurationCancelled
	}
	return values, nil
}

// A unique comparable marker keeps clear distinct from JSON null.
var clearConfigurationValue = &struct{}{}

func (w configurationWizard) field(name string, schema map[string]any, old any, hasOld, required bool) (any, bool, error) {
	secret := w.sensitive(schema, map[string]bool{}) || hasConfigurationSecret(old)
	kind, _ := schema["type"].(string)
	if kind == "" {
		if schema["properties"] != nil {
			kind = "object"
		} else {
			kind = "JSON"
		}
	}
	requirement := "optional"
	if required {
		requirement = "required"
	}
	label := name
	if title, ok := schema["title"].(string); ok && title != "" {
		label += " — " + title
	}
	if secret {
		requirement += ", secret; input hidden"
	}
	fmt.Fprintf(w.out, "\n%s (%s, %s)\n", label, kind, requirement)
	if description, ok := schema["description"].(string); ok {
		fmt.Fprintln(w.out, description)
	}
	if choices := schema["enum"]; choices != nil && !secret {
		encoded, _ := json.Marshal(choices)
		fmt.Fprintf(w.out, "Choices: %s\n", encoded)
	}
	fallback, hasDefault := schema["default"]
	if hasOld {
		if secret {
			fmt.Fprintln(w.out, "Saved value: [hidden]")
		} else {
			encoded, _ := json.Marshal(old)
			fmt.Fprintf(w.out, "Saved value: %s\n", encoded)
		}
	}
	if hasDefault {
		if secret {
			fmt.Fprintln(w.out, "Default available: [hidden]")
		} else {
			encoded, _ := json.Marshal(fallback)
			fmt.Fprintf(w.out, "Default: %s\n", encoded)
		}
	}
	for {
		fmt.Fprint(w.out, "> ")
		line, err := w.read(secret)
		if err != nil {
			return nil, false, err
		}
		switch line {
		case ":cancel":
			return nil, false, errConfigurationCancelled
		case ":clear":
			if required {
				fmt.Fprintln(w.out, "This field is required and cannot be cleared.")
				continue
			}
			return clearConfigurationValue, false, nil
		case "":
			if hasOld {
				return nil, true, nil
			}
			if hasDefault {
				return fallback, false, nil
			}
			if !required {
				return nil, true, nil
			}
			fmt.Fprintln(w.out, "A value is required.")
			continue
		}
		var value any
		if kind == "string" {
			value = line
			if line == ":empty" {
				value = ""
			}
		} else {
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			var extra any
			if err := decoder.Decode(&value); err != nil || decoder.Decode(&extra) != io.EOF {
				fmt.Fprintln(w.out, "Enter a valid JSON value of the indicated type.")
				continue
			}
		}
		if !validConfigurationValue(schema, value) {
			fmt.Fprintln(w.out, "Value does not match the field type, choices or length requirements.")
			continue
		}
		return value, false, nil
	}
}

func validConfigurationValue(schema map[string]any, value any) bool {
	if choices, ok := schema["enum"].([]any); ok {
		encoded, _ := json.Marshal(value)
		found := false
		for _, choice := range choices {
			other, _ := json.Marshal(choice)
			found = found || string(other) == string(encoded)
			if a, ok := configurationNumber(value); ok {
				if b, ok := configurationNumber(choice); ok && a == b {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	switch schema["type"] {
	case "string":
		v, ok := value.(string)
		if !ok {
			return false
		}
		length := float64(utf8.RuneCountInString(v))
		if n, ok := configurationNumber(schema["minLength"]); ok && length < n {
			return false
		}
		if n, ok := configurationNumber(schema["maxLength"]); ok && length > n {
			return false
		}
	case "number", "integer":
		n, ok := configurationNumber(value)
		if !ok {
			return false
		}
		if schema["type"] == "integer" && n != math.Trunc(n) {
			return false
		}
		if min, ok := configurationNumber(schema["minimum"]); ok && n < min {
			return false
		}
		if max, ok := configurationNumber(schema["maximum"]); ok && n > max {
			return false
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return false
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return false
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return false
		}
	case "null":
		return value == nil
	}
	return true
}

func configArray(value any) []any { result, _ := value.([]any); return result }

func (w configurationWizard) schema(value any, seen map[string]bool) (map[string]any, error) {
	source, _ := value.(map[string]any)
	result := maps.Clone(source)
	if result == nil {
		result = map[string]any{}
	}
	merge := func(other map[string]any) {
		for key, value := range other {
			if key == "properties" {
				properties, _ := result[key].(map[string]any)
				properties = maps.Clone(properties)
				if properties == nil {
					properties = map[string]any{}
				}
				child, _ := value.(map[string]any)
				maps.Copy(properties, child)
				result[key] = properties
			} else if key == "required" {
				result[key] = append(slices.Clone(configArray(result[key])), configArray(value)...)
			} else if result[key] == nil {
				result[key] = value
			}
		}
	}
	if reference, ok := source["$ref"].(string); ok {
		if seen[reference] {
			return nil, fmt.Errorf("recursive configuration schema needs a nonrecursive form for interactive setup")
		}
		target, err := w.reference(reference)
		if err != nil {
			return nil, err
		}
		next := maps.Clone(seen)
		next[reference] = true
		resolved, err := w.schema(target, next)
		if err != nil {
			return nil, err
		}
		merge(resolved)
	}
	for _, branch := range configArray(source["allOf"]) {
		resolved, err := w.schema(branch, seen)
		if err != nil {
			return nil, err
		}
		merge(resolved)
	}
	return result, nil
}

func (w configurationWizard) reference(reference string) (any, error) {
	if reference == "#" {
		return w.root, nil
	}
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("interactive configuration supports local schema references only")
	}
	var target any = w.root
	for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		object, ok := target.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid configuration schema reference")
		}
		target, ok = object[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
		if !ok {
			return nil, fmt.Errorf("invalid configuration schema reference")
		}
	}
	return target, nil
}

func (w configurationWizard) sensitive(value any, seen map[string]bool) bool {
	switch value := value.(type) {
	case map[string]any:
		if value["x-vortexify-secret"] == true {
			return true
		}
		if ref, ok := value["$ref"].(string); ok && !seen[ref] {
			seen[ref] = true
			target, err := w.reference(ref)
			if err != nil || w.sensitive(target, seen) {
				return true
			}
		}
		for _, child := range value {
			if w.sensitive(child, seen) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if w.sensitive(child, seen) {
				return true
			}
		}
	}
	return false
}

func hasConfigurationSecret(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, exists := value["$secret"]; exists {
			return true
		}
		for _, child := range value {
			if hasConfigurationSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasConfigurationSecret(child) {
				return true
			}
		}
	}
	return false
}

func configurationNumber(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case json.Number:
		result, err := value.Float64()
		return result, err == nil
	}
	return 0, false
}
