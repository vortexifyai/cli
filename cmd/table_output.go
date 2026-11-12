package cmd

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"vortexify-cli/internal/api"
)

// Wrap saved prose without rewriting or summarizing the findings.
func tableProse(out io.Writer, indent, text string) {
	for _, paragraph := range strings.Split(strings.TrimSpace(text), "\n") {
		line := indent
		for _, word := range strings.Fields(paragraph) {
			if line != indent && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > 96 {
				fmt.Fprintln(out, line)
				line = indent
			}
			if line != indent {
				line += " "
			}
			line += word
		}
		fmt.Fprintln(out, line)
	}
}

func printTableDetails(out io.Writer, details *api.TableDetails, names map[string]string, verbose bool) error {
	if details.Table.ResourceReference != "" {
		fmt.Fprintf(out, "Reference: %s\n", details.Table.ResourceReference)
	}
	var b bytes.Buffer
	table := details.Table
	graph, keys, relationships := tableGraphStatuses(table.KnowledgeGraph)
	data, schema := tableDataSchemaStatuses(table)
	fmt.Fprintln(&b, tableLabel(table))
	if strings.TrimSpace(table.Description) != "" {
		tableProse(&b, "  ", table.Description)
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "  Data: %s · Schema: %s · Knowledge graph: %s\n", data, schema, graph)
	if version := details.Publication["source_resource_version"]; version != nil {
		fmt.Fprintf(&b, "  Producer version: %v\n", version)
	}
	if verbose {
		fmt.Fprintf(&b, "  ID: %s\n  Source: %s\n", table.ID, table.Type)
	}
	fmt.Fprintf(&b, "\nCandidate keys: %s\n", strings.ReplaceAll(keys, "ready", "complete"))
	if analysis := details.KeyAnalysis; analysis != nil {
		for _, key := range analysis.Keys {
			fmt.Fprintf(&b, "  %s — %.2f%% unique\n", strings.Join(key.Columns, ", "), key.UniquePercentage)
			if verbose && key.GrainDescription != "" {
				tableProse(&b, "    ", "Grain: "+key.GrainDescription)
			}
			if verbose && key.DuplicatesReason != "" {
				tableProse(&b, "    ", "Duplicates: "+key.DuplicatesReason)
			}
		}
		if len(analysis.Keys) == 0 && analysis.Completed {
			fmt.Fprintln(&b, "  None found.")
		}
		if verbose && analysis.GrainDescription != "" {
			tableProse(&b, "  ", "Table grain: "+analysis.GrainDescription)
		}
	}
	fmt.Fprintf(&b, "\nRelationships: %s\n", strings.ReplaceAll(relationships, "ready", "complete"))
	pairs, count := 0, 0
	for _, evaluation := range details.RelationshipEvaluations {
		if evaluation.Status == "completed" {
			pairs++
		}
		count += len(evaluation.Relationships)
	}
	if len(details.RelationshipEvaluations) > 0 {
		pairLabel, relationshipLabel := "pairs", "relationships"
		if pairs == 1 {
			pairLabel = "pair"
		}
		if count == 1 {
			relationshipLabel = "relationship"
		}
		fmt.Fprintf(&b, "  %d %s evaluated · %d %s found\n", pairs, pairLabel, count, relationshipLabel)
	} else if relationships == "ready" {
		fmt.Fprintln(&b, "  None found.")
	}
	tableName := func(id string) string {
		if name := names[id]; name != "" {
			return strings.TrimSuffix(name, "/default")
		}
		return "unavailable table (" + id + ")"
	}
	columnReference := func(id string, columns []string) string {
		column := strings.Join(columns, ", ")
		if len(columns) != 1 {
			column = "(" + column + ")"
		}
		if id != table.ID {
			return tableName(id) + "." + column
		}
		return column
	}
	for _, evaluation := range details.RelationshipEvaluations {
		fmt.Fprintf(&b, "\n  %s", tableName(evaluation.OtherTableID))
		if evaluation.Status != "completed" {
			fmt.Fprintf(&b, " — %s", evaluation.Status)
		}
		fmt.Fprintln(&b)
		for _, relation := range evaluation.Relationships {
			fmt.Fprintf(&b, "    %s → %s  %s\n", columnReference(relation.SourceTableID, relation.SourceColumns), columnReference(relation.TargetTableID, relation.TargetColumns), strings.ReplaceAll(relation.Cardinality, "_", "-"))
			if relation.Description != "" {
				tableProse(&b, "      ", relation.Description)
			}
			if verbose && relation.RelationshipType != "" {
				tableProse(&b, "      ", "Type: "+relation.RelationshipType)
			}
			if verbose && relation.MatchingLogic != "" {
				tableProse(&b, "      ", "Matching logic: "+relation.MatchingLogic)
			}
		}
		if len(evaluation.Relationships) == 0 && evaluation.Status == "completed" {
			if evaluation.Outcome == "no_relationship_found" {
				fmt.Fprintln(&b, "    No relationship found.")
			} else {
				outcome := strings.ReplaceAll(evaluation.Outcome, "_", " ")
				if outcome == "" {
					outcome = "No saved relationships."
				}
				fmt.Fprintf(&b, "    %s\n", outcome)
			}
		}
		if verbose && evaluation.Reason != "" {
			tableProse(&b, "    ", "Reason: "+evaluation.Reason)
		}
		if evaluation.LastError != "" {
			tableProse(&b, "    ", "Error: "+evaluation.LastError)
		}
	}
	if state := table.KnowledgeGraph; state != nil && state.WaitingReason != "" {
		fmt.Fprintln(&b)
		tableProse(&b, "  ", "Knowledge graph waiting: "+state.WaitingReason)
	}
	if state := table.KnowledgeGraph; state != nil && state.Error != "" {
		fmt.Fprintln(&b)
		tableProse(&b, "  ", "Knowledge graph error: "+state.Error)
	}
	if table.Error != "" {
		fmt.Fprintln(&b)
		tableProse(&b, "  ", "Data error: "+table.Error)
	}
	if err := printTableJobs(&b, details); err != nil {
		return err
	}
	_, err := b.WriteTo(out)
	return err
}
