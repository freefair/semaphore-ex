package util

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenPortErrorRedaction(t *testing.T) {
	cases := []struct{ name, jsonValue, yamlValue, forbidden string }{
		{"bool", `true`, `true`, `true`},
		{"fraction", `30.5`, `30.5`, `30.5`},
		{"object", `{"secret":"synthetic-sentinel"}`, `{secret: synthetic-sentinel}`, `synthetic-sentinel`},
		{"array", `["synthetic-sentinel"]`, `[synthetic-sentinel]`, `synthetic-sentinel`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var port ListenPort
			err := json.Unmarshal([]byte(tc.jsonValue), &port)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), tc.forbidden)
			for _, format := range []string{"json", "yaml"} {
				t.Run(format, func(t *testing.T) {
					old := Config
					t.Cleanup(func() { Config = old })
					Config = new(ConfigType)
					body := `{"port":` + tc.jsonValue + `}`
					if format == "yaml" {
						body = "port: " + tc.yamlValue + "\n"
					}
					var recovered any
					func() {
						defer func() { recovered = recover() }()
						decodeConfig(strings.NewReader(body), "config."+format)
					}()
					require.NotNil(t, recovered)
					assert.NotContains(t, fmt.Sprint(recovered), tc.forbidden)
				})
			}
		})
	}
}

func TestListenPortConfigurationCompatibility(t *testing.T) {
	cases := []struct {
		name, body, format, environment, address string
	}{
		{"json integer", `{"port":8080}`, "json", "", ":8080"},
		{"yaml integer", "port: 8080\n", "yaml", "", ":8080"},
		{"json string", `{"port":"8080"}`, "json", "", ":8080"},
		{"yaml colon", "port: ':8080'\n", "yaml", "", ":8080"},
		{"default", `{}`, "json", "", ":3000"},
		{"null default", `{"port":null}`, "json", "", ":3000"},
		{"empty default", `{"port":""}`, "json", "", ":3000"},
		{"environment override", `{"port":8080}`, "json", "9090", ":9090"},
		{"colon override", "port: 8080\n", "yaml", ":9090", ":9090"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := Config
			t.Cleanup(func() { Config = old })
			Config = new(ConfigType)
			t.Setenv("SEMAPHORE_PORT", tc.environment)
			decodeConfig(strings.NewReader(tc.body), "config."+tc.format)
			// Exercise the production setter without importing unrelated host environment values.
			if tc.environment != "" {
				setConfigValue(reflect.ValueOf(Config).Elem().FieldByName("Port"), tc.environment)
			}
			loadConfigDefaults()
			assert.Equal(t, tc.address, Config.Port.Addr())
			assert.Equal(t, "http://localhost"+tc.address, GetPublicHost())
			Config.WebHost = "https://semaphore.example.test/prefix"
			assert.Equal(t, Config.WebHost, GetPublicHost())
			encoded, err := json.Marshal(Config.Port)
			require.NoError(t, err)
			var serialized string
			require.NoError(t, json.Unmarshal(encoded, &serialized))
			assert.Equal(t, Config.Port.String(), serialized)
		})
	}
}

func TestListenPortIntegerValidation(t *testing.T) {
	for _, value := range []string{"-1", "100000"} {
		t.Run(value, func(t *testing.T) {
			old := Config
			t.Cleanup(func() { Config = old })
			Config = new(ConfigType)
			decodeConfig(strings.NewReader(`{"port":`+value+`}`), "config.json")
			loadConfigDefaults()
			err := validate(Config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "'Port'")
		})
	}
}
