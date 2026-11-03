package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ConnectorConfiguration struct {
	Configuration map[string]any `json:"configuration"`
	Schema        any            `json:"configuration_schema"`
	Version       int            `json:"version"`
}

func (c *Client) StartConnectorOperation(ctx context.Context, projectID, connectorID, action string, version int) (Execution, error) {
	endpoint := ""
	switch action {
	case "check":
		endpoint = "check_connection"
	case "discover":
		endpoint = "discover"
	default:
		return nil, fmt.Errorf("unsupported connector operation %q", action)
	}
	values := map[string]any{"connector_id": connectorID}
	if version > 0 {
		values["version"] = version
	}
	response, err := c.Service(ctx, "data/connectors/"+endpoint, projectID, values)
	if err != nil {
		return nil, err
	}
	result, err := decode[Execution](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

func (c *Client) GetConnectorConfiguration(ctx context.Context, projectID, connectorID string) (*ConnectorConfiguration, error) {
	response, err := c.Service(ctx, "data/connectors/get_configuration", projectID, map[string]any{"connector_id": connectorID})
	if err != nil {
		return nil, err
	}
	return decodeConnectorConfiguration(response)
}

func (c *Client) ConfigureConnector(ctx context.Context, projectID, connectorID string, values map[string]any) (*ConnectorConfiguration, error) {
	response, err := c.Service(ctx, "data/connectors/configure", projectID, map[string]any{"connector_id": connectorID, "configuration": values})
	if err != nil {
		return nil, err
	}
	return decodeConnectorConfiguration(response)
}

// Preserve integer configuration values exactly when users keep saved fields.
func decodeConnectorConfiguration(response *http.Response) (*ConnectorConfiguration, error) {
	defer response.Body.Close()
	var value ConnectorConfiguration
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return &value, nil
}
