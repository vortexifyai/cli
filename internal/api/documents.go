package api

import (
	"context"
	"fmt"
	"strings"
	"vortexify-cli/internal/resources"
)

type Document struct {
	ID      string `json:"id"`
	Type    string `json:"document_type"`
	Content string `json:"document"`
}

func (c *Client) GetDocument(ctx context.Context, projectID, documentType, resource string) (*Document, error) {
	values := map[string]any{}
	if resource != "" {
		rows, err := c.GetResourceGraph(ctx, projectID)
		if err != nil {
			return nil, err
		}
		kind := strings.SplitN(documentType, "_", 2)[0]
		if documentType == "plan" {
			kind = ""
		}
		selectionKind := kind
		if kind == "function" {
			selectionKind = ""
		}
		entries := []resources.RegistryEntry{}
		for _, row := range rows {
			entries = append(entries, resources.RegistryEntry{ID: row.ID, Type: row.Type, Name: row.Name})
		}
		if err := (resources.Filter{Type: selectionKind, Resource: resource}).CheckSelection(entries); err != nil {
			return nil, err
		}
		matches := 0
		for _, row := range rows {
			attachment := sourceKind(row.Type)
			if row.Type == "table" || row.Type == "secret" || (kind != "" && attachment != kind) || !(resources.Filter{Type: row.Type, Resource: resource}).Match(row.Type, row.Name, row.ID) {
				continue
			}
			matches++
			values[attachment+"_id"] = row.ID
			if documentType == "plan" {
				values["document_type"] = attachment + "_plan"
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("document requires exactly one matching resource (found %d); use a resource ID or a specific document type", matches)
		}
	} else if documentType == "plan" || documentType == "requirements" {
		documentType = "project_requirements"
	}
	if values["document_type"] == nil {
		values["document_type"] = documentType
	}
	resp, err := c.Service(ctx, "documents/documents/get_project_documents", projectID, values)
	if err != nil {
		return nil, err
	}
	documents, err := decode[[]Document](resp)
	if err != nil {
		return nil, err
	}
	if len(*documents) != 1 {
		return nil, fmt.Errorf("expected one document, found %d; specify --resource for resource plans", len(*documents))
	}
	return &(*documents)[0], nil
}
