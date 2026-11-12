package projectfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Instructions = `<!-- vortexify-project-context -->
## Vortexify project context and business knowledge

Use these collections as a persistent, evolving understanding of the project and
business. Read .vortexify/context/index.md and .vortexify/knowledge-base/index.md
first, then follow relevant links before planning or changing resources.

### What belongs where

Context describes what we are building, why, how it should work, and how we plan
to implement it:

- context/requirements/: Markdown project objectives, user needs, constraints and
  acceptance criteria. One project-requirements.md is often enough; split only
  when useful, rather than creating a requirements file for every business topic.
- context/applications/: Markdown application specifications describing purpose,
  users, pages, workflows, interactions and expected functionality; actual HTML
  designs showing layouts, components, charts and interactions when useful.
- context/architecture/: Markdown implementation plans and decisions explaining
  pipelines, calculations, APIs, responsibilities and how resources work together.
  One plan can cover several resources.

Knowledge Base describes the underlying business:

- knowledge-base/concepts/: Markdown business concepts, definitions, processes,
  calculations, policies and rules, such as available inventory or waste classification.
- knowledge-base/entities/: Markdown business entities, such as production orders,
  machines or doctors: what they represent and how they relate. An entity may span
  multiple datasets; it is not a document per table.

These paths are inside .vortexify/. Use .md for documents and .html for designs;
HTML designs belong under context/applications/. Prefer app-name-prefixed names
such as inventory-planner-overview.html. Multiple designs per app are supported.
Designs are useful visual implementation references, not just written descriptions.
Attached blueprint examples are separate references, read through vortexify blueprints.

### When to maintain the collections

- Update Context as users explain requirements, applications are planned or
  designed, resources are implemented, and requirements or decisions change.
  Maintain useful specifications, HTML designs and architecture alongside the work,
  rather than waiting for a separate documentation step at the end.
- Update Knowledge Base when users provide business explanations, answer business
  questions, or data exploration reveals meaningful business understanding.
  Preserve that learning even when the user did not explicitly ask to document it.
  A code change alone does not require a Knowledge Base update.
- Use primary-key and relationship findings to understand business entities and
  their connections. Synthesize useful meaning; do not copy graph reports, schema
  dumps or connector table catalogs. Inspect source, CLI tables/previews and the
  knowledge graph directly for current technical information.
- Distinguish confirmed facts from observations and unresolved interpretations.
  Preserve useful open questions and revisit them when relevant without interrupting
  for every uncertainty. Update existing knowledge when clarified; preserve business
  explanations when discovery findings change. Neither collection is a transcript
  or activity log.

### Organization and maintenance

Both root index.md files and all five category folders are required; folders may
be empty. Keep indexes concise, with overviews and links to useful documents.
Create files and nested folders only when they help, for example
knowledge-base/entities/manufacturing/production-order.md or
context/applications/designs/inventory-planner-overview.html. Additional indexes
are optional. No prescribed sections, document templates or document per resource
are required.

Prefer updating existing documents. Split large or distinct topics, merge overlap,
and remove obsolete plans and superseded designs while preserving unrelated knowledge.
Keep the smallest useful collection of accurate knowledge and development artifacts.
Maintain relative Markdown links BETWEEN documents when files are added, moved or
deleted. Resolve them from the actual document location, including .vortexify/.
For registered resources, use canonical Markdown links instead of local source
paths. Copy resource_reference from vortexify registry --json or vortexify tables
--json, for example [Plants](vortexify://table/sync/mes/plants) or
[Forecast](vortexify://transform/forecast). These open the current project's
resource/table view, independent of local YAML layouts. Do not invent links from
output names such as default or UUIDs. If a reference is absent, report the gap
and leave the name as prose. Missing or ambiguous targets show an error.
Links do not pin versions, run resources or automatically synchronize targets.

Plans express intent; committed code and registered resources establish implemented
behavior. Reconcile differences and distinguish planned from completed work. Do not
start unrelated planned work merely because a document mentions it.

Use vortexify context status, pull and push for the collections; full vortexify
status, pull and commit also include them. Collections are versioned separately
from resource source and runtime-agent attachments. Reconcile concurrent changes
before saving again. Inspect command help for the installed CLI version.
<!-- /vortexify-project-context -->
`

func WriteInstructions(root string) error {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		text := string(data)
		start := strings.Index(text, "<!-- vortexify-project-context -->")
		if start >= 0 {
			end := strings.Index(text[start:], "<!-- /vortexify-project-context -->")
			if end < 0 {
				return fmt.Errorf("unterminated managed project guidance in %s", name)
			}
			end += start + len("<!-- /vortexify-project-context -->")
			text = text[:start] + strings.TrimSuffix(Instructions, "\n") + text[end:]
		} else {
			text += "\n" + Instructions
		}
		if err = os.WriteFile(path, []byte(text), 0644); err != nil {
			return err
		}
	}
	return nil
}
