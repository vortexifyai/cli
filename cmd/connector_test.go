package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/config"
)

func wizardForTest(t *testing.T, schemaJSON string, answers []string) (configurationWizard, *bytes.Buffer, *[]bool) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	hidden := []bool{}
	position := 0
	return configurationWizard{root: schema, out: out, read: func(secret bool) (string, error) {
		hidden = append(hidden, secret)
		if position >= len(answers) {
			return "", io.EOF
		}
		answer := answers[position]
		position++
		return answer, nil
	}}, out, &hidden
}

func TestConfigureRequiredDefaultsTypedValuesAndMaskedSecrets(t *testing.T) {
	wizard, out, hidden := wizardForTest(t, `{"type":"object","properties":{
        "host":{"type":"string","minLength":1,"order":0,"description":"Server host"},
        "api_key":{"type":"string","x-vortexify-secret":true,"order":1},
        "port":{"type":"integer","default":443,"order":2},
        "enabled":{"type":"boolean","order":3},
        "unused":{"type":"string","order":4}},"required":["host","api_key"]}`,
		[]string{"", "example.com", "top-secret-value", "", "false", "", "yes"})
	values, err := wizard.run(nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["host"] != "example.com" || values["port"] != float64(443) || values["enabled"] != false || values["api_key"] != "top-secret-value" {
		t.Fatal("incorrect values")
	}
	if _, exists := values["unused"]; exists {
		t.Fatal("optional field was not skipped")
	}
	if !(*hidden)[2] || strings.Contains(out.String(), "top-secret-value") || !strings.Contains(out.String(), "required, secret; input hidden") || !strings.Contains(out.String(), "A value is required") {
		t.Fatal("secret or required-field prompting failed")
	}
}

func TestConfigurePreservesSecretReferencesAndClearsOptionalValues(t *testing.T) {
	wizard, out, _ := wizardForTest(t, `{"type":"object","properties":{
        "token":{"type":"string","x-vortexify-secret":true,"order":0},
        "region":{"type":"string","order":1},"nullable":{"type":["string","null"],"order":2}},"required":["token","nullable"]}`,
		[]string{"", ":clear", "", "y"})
	reference := map[string]any{"$secret": map[string]any{"secret_id": "private-secret-id", "secret_key": "value_0"}}
	previous := map[string]any{"token": reference, "region": "old-region", "nullable": nil}
	values, err := wizard.run(previous)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(values["token"])
	expected, _ := json.Marshal(reference)
	if string(data) != string(expected) {
		t.Fatal("secret reference was changed")
	}
	if _, exists := values["region"]; exists {
		t.Fatal("clear did not remove field")
	}
	if _, exists := values["nullable"]; !exists {
		t.Fatal("saved null was not retained")
	}
	if previous["region"] != "old-region" || strings.Contains(out.String(), "private-secret-id") {
		t.Fatal("existing configuration was mutated or exposed")
	}
}

func TestConfigureNestedSecretsReferencesAndCancellation(t *testing.T) {
	wizard, out, hidden := wizardForTest(t, `{"type":"object","$defs":{"auth":{"type":"object","properties":{"key":{"type":"string","x-vortexify-secret":true}}}},"properties":{"auth":{"$ref":"#/$defs/auth"}}}`,
		[]string{`{"key":"nested-secret"}`, "n"})
	_, err := wizard.run(nil)
	if !errors.Is(err, errConfigurationCancelled) || !(*hidden)[0] || strings.Contains(out.String(), "nested-secret") {
		t.Fatal("nested credential masking or cancellation failed")
	}
	wizard, _, _ = wizardForTest(t, `{"type":"object","properties":{"key":{"type":"string"}}}`, []string{":cancel"})
	if _, err = wizard.run(nil); !errors.Is(err, errConfigurationCancelled) {
		t.Fatal(err)
	}
}

func TestConfigureComposedSchemaMasksWholeObject(t *testing.T) {
	wizard, out, hidden := wizardForTest(t, `{"type":"object","properties":{"mode":{"type":"string"}},"oneOf":[{"properties":{"password":{"type":"string","x-vortexify-secret":true}}}]}`,
		[]string{`{"mode":"password","password":"composition-secret"}`, "y"})
	values, err := wizard.run(nil)
	if err != nil || values["mode"] != "password" || !(*hidden)[0] || strings.Contains(out.String(), "composition-secret") {
		t.Fatal("composed-schema credential was exposed", err)
	}
}

func TestConfigureRequiresTerminalForSecretEntry(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("must-not-be-read\n"))
	if _, err := configurationReader(cmd)(true); err == nil {
		t.Fatal("secret input silently used an echoing reader")
	}
}

func TestConnectorConfigureUsesExistingServerServices(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "save"}[confirm], func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("VORTEXIFY_TOKEN", "token")
			if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
				t.Fatal(err)
			}
			saves := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if r.Method != "POST" || body["org_id"] != "org" || body["project_id"] != "project" {
					t.Error("incorrect request scope")
				}
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					io.WriteString(w, `[{"id":"connector-id","type":"connector","name":"source"}]`)
				case "/api/v2/data/connectors/get_configuration":
					if body["connector_id"] != "connector-id" {
						t.Error("wrong connector")
					}
					io.WriteString(w, `{"configuration":{"host":"old-host"},"configuration_schema":{"type":"object","properties":{"host":{"type":"string"}}}}`)
				case "/api/v2/data/connectors/configure":
					saves++
					values, _ := body["configuration"].(map[string]any)
					if body["connector_id"] != "connector-id" || values["host"] != "new-host" {
						t.Error("wrong configuration")
					}
					io.WriteString(w, `{"configuration":{"host":"new-host"},"version":3}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			cmd := NewRootCommand()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)
			answer := "n"
			if confirm {
				answer = "y"
			}
			cmd.SetIn(strings.NewReader("new-host\n" + answer + "\n"))
			cmd.SetArgs([]string{"--api-url", server.URL, "--org-id", "org", "connector", "source", "--configure"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if confirm {
				expected = 1
			}
			if saves != expected {
				t.Fatal("saved before confirmation", saves)
			}
			if confirm && !strings.Contains(out.String(), "version 3") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestConfigurePreservesLargeIntegerInput(t *testing.T) {
	wizard, _, _ := wizardForTest(t, `{"type":"object","properties":{"id":{"type":"integer"}}}`, []string{"9007199254740993", "y"})
	values, err := wizard.run(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(values["id"])
	if string(encoded) != "9007199254740993" {
		t.Fatal("integer was rounded", string(encoded))
	}
}
