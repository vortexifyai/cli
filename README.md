# Vortexify CLI

Build and manage Vortexify projects from your terminal. Create resources, synchronize code, run data workflows, develop React apps, and deploy saved versions.

## Changelog

### v0.1.8

- Add project guardian status, wait and retry commands, and show new starter resources as uncommitted until their first explicit commit.
- Expose API return values at `response`; add `run api --response-only` and keep migration/job output focused on public status fields.
- Show the latest database migration attempt per environment, authenticated app preview links and published deployment URLs. Omit automatic validation status for APIs.
- Include preview database tables in inventory, resolve SQL analysis to preview by default or origin with `--deployed`, and show detailed key/relationship findings.
- Distinguish excluded knowledge graph tables from pending work and omit frontend recommendation cards from blueprint-search JSON while retaining citations and YAML output.
- Clarify saved-version mock testing and API runtime preparation.

### v0.1.7

- Expose canonical resource references in registry and table output.
- Accept canonical table references in table commands.
- Warn about workspace-dependent links in project documents.
- Update generated project instructions with portable resource-link conventions.

### v0.1.6

- Add `skills install --agent-path` for selecting a Codex or Claude CLI executable, including desktop-bundled binaries outside PATH. Keep PATH lookup as the default and explain the override when the executable cannot be found. `--dry-run` prints the selected executable without requiring it to be installed.
- Save blueprint-wide and per-part usage notes with `blueprints attach --usage-note ... --part ... --part-note PART_ID=NOTE`. Repeat `--part` and `--part-note` for multiple parts; omitted fields and notes stay unchanged. Empty notes clear the corresponding note, and deselecting a part removes its note.
- Use the current workspace as blueprint-search project context, or select one with `--project-id`. Search remains available outside a workspace. The discovery agent can inspect design code and attach references when explicitly requested.
- Remove blueprint-search taxonomy flags: `--industry`, `--subindustry`, `--category`, `--subcategory`, `--module`, `--solution-type`, and `--use-case`. Put these preferences in the search question instead; `--conversation` still continues a saved search.

### v0.1.5

- Add `deploy sync/transform --run` and `upgrade sync/transform --run` to execute in production and activate the selected version after successful output publication. Accept runtime inputs with `--inputs` or `--inputs-file` on deploy and upgrade.
- Support `run sync --production` alongside production transform runs. Production runs default to the deployed version; ordinary development runs default to the latest committed version.
- Report deployment activation, sync mode and a missing production checkpoint for deployment runs. With the updated server/runtime, development and production table outputs and sync checkpoints are separate per committed version; deployment does not copy preview data or checkpoints.
- Add conversational project/app and blueprint discovery. Refine a search with `--conversation <id>`; omit the flag to start fresh. Browse saved searches in the app UI.
- Allow organization-scoped blueprint searches without a project workspace, with category, subcategory, module and subindustry filters. Return specific blueprint part recommendations with reasons and citations.
- Allow longer discovery requests when the assistant needs multiple tool calls.

## Installation

Download the installer for your computer:

- [macOS / Linux: install.sh](https://github.com/vortexifyai/cli/releases/latest/download/install.sh)
- [Windows AMD64: install.ps1](https://github.com/vortexifyai/cli/releases/latest/download/install.ps1)

On macOS or Linux, open Terminal and run the downloaded file:

```sh
sh ~/Downloads/install.sh
```

It downloads and verifies the matching CLI executable and installs to `~/.local/bin`. Add that directory to PATH if the installer asks. Use `--install-dir /path/to/bin` for another destination.

On Windows, open PowerShell and run the downloaded file:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$HOME\Downloads\install.ps1"
```

It downloads and verifies the Windows executable, installs to `%LOCALAPPDATA%\Vortexify\bin`, and adds it to your user PATH without administrator access. Open a new terminal afterward. Use `-InstallDir C:\Tools\Vortexify` for another destination or `-NoPathUpdate` to manage PATH yourself.

Both installers already know their release's download URL. You do not need to supply one.

```sh
vortexify --version
```

Project source inspection requires Python 3.9 or newer available as `python3`. Local React app development also requires Node.js and npm.

## Update the CLI

Check whether a newer release is available:

```sh
vortexify update --check
```

Install the latest release:

```sh
vortexify update
```

The updater verifies and runs the installer from that release, keeping the current CLI installation directory. On Windows, installation finishes in a separate PowerShell window after the current CLI exits. Close other running Vortexify commands before updating. After it finishes, use `vortexify --version` to confirm the installed version.

## Get started

Sign in through your browser, then create a project:

```sh
vortexify login
vortexify init operations
cd operations
vortexify create transform orders
vortexify status
```

`init` asks you to select an organization and creates a new directory with the name you provide. Run it from the directory where you want the project folder created.

To work with an existing project:

```sh
vortexify projects list
vortexify projects search "operations"
vortexify projects search "Which one is deployed?" --conversation <conversation-id>
vortexify init operations --project-id PROJECT_ID
cd operations
vortexify pull
```

Use `--org-id ORGANIZATION_ID` to select an organization explicitly in scripts. Project commands work from the project root or its subdirectories.

## Create and synchronize resources

```sh
vortexify create api customer_orders
vortexify create app dashboard
vortexify create connector shop
vortexify create sync customers --connector shop

vortexify status
vortexify commit
vortexify pull
```

`create` registers a resource and supplies starter code. Edit its files, inspect changes with `status`, and save a new version with `commit`. The first commit creates version 1, even if the starter is unchanged. Use `pull` to retrieve saved code and project documents.

You can select individual resources:

```sh
vortexify status --type transform --resource orders
vortexify commit --type api --resource customer_orders
vortexify pull --type app --resource dashboard --include-dependencies
```

Supported resource types include `api`, `transform`, `agent`, `connector`, `sync`, `database`, and `app`. Syncs belong to connectors; use a qualified name such as `shop/customers` when selecting one.

Commit can save independent resources even when others have validation issues. A partial commit lists skipped resources and exits with a nonzero status. Resolve the reported issues and rerun it; successfully committed changes are retained.

Pull preserves local edits and reports conflicts. If a synchronization operation was interrupted, run `vortexify recover` before continuing. Keep the CLI-managed `.vortexify/` state out of Git.

## Run and test workflows

Commit your changes before running them. Execution uses saved code, not uncommitted local edits.

```sh
vortexify run api customer_orders --inputs '{"customer_id":"123"}' --wait
vortexify run transform orders --wait
# Run the deployed transform against production inputs and origin databases:
vortexify run transform orders --production --wait
vortexify run sync shop/customers --wait
vortexify test transform orders --wait
vortexify test api customer_orders --wait
```

`vortexify test` runs mock tests against committed source. Use `--version N` to select a saved version and inspect the test results for that job.

Supply explicit mocks for resource inputs and write assertions for expected columns and business rules. Missing required mocks are errors. Use `vortexify run` for integration checks with real preview data, or `vortexify.testing.run_tests(...)` for local test feedback.

APIs should implement required request and response checks in their code or tests. Automatic output validation is available for transforms.

API runs expose the actual return value at the top-level `response` key in `--json` output, alongside job status. Successful null returns have `"response": null`; pending or failed runs omit that key. To wait and print only the API value as JSON (without progress or job metadata):

```sh
vortexify run api customer_orders --inputs '{"customer_id":"123"}' --response-only
```

`--response-only` applies to `run api`, implies waiting, and cannot be combined with `--json`. Failed or stopped runs exit nonzero without printing a response.

Use `--inputs-file inputs.json` instead of inline JSON for larger parameter sets. `--version VERSION` selects a specific saved version.

`--wait` displays progress until completion. Without it, the command returns a job ID you can inspect later:

```sh
vortexify job JOB_ID
vortexify job JOB_ID --wait
vortexify logs JOB_ID
```

`--timeout 30m` changes how long the CLI waits. Stopping the CLI or timing out does not cancel the submitted job. Failed or stopped jobs return a nonzero status when waiting for their results.

To rebuild affected downstream previews:

```sh
vortexify run transform orders --rebuild-downstream --wait
```

App `--build-memory` accepts 1–8192 MiB and saves the setting for subsequent builds. App deployment completes once both sites have been built and their CloudFront/DNS publications accepted; the URL may take a few minutes to become ready.

Preview work does not deploy resources or trigger production schedules. Use `status` to see whether previews are current or need rebuilding.

## Develop a React app locally

Install your app's dependencies once:

```sh
cd apps/dashboard
npm install
```

Then, from your project:

```sh
vortexify dev dashboard
```

Open the URL printed by **Vortexify**, rather than Vite's separate startup URL. The CLI starts your app and supplies its project configuration and authenticated connection to Vortexify. The app does not need manually configured SDK environment variables or browser tokens.

The React SDK setup in your app remains:

```tsx
const config = createVortexifyConfig(import.meta.env);
<VortexifyProvider config={config}><App /></VortexifyProvider>
```

Inside an app directory, `vortexify dev` can select that app automatically. Use `--port 5174` to choose a port. Commit APIs and prepare any required data before calling them from your local app.

## Build and deploy

```sh
vortexify build api customer_orders --wait
vortexify build app dashboard --wait
vortexify deploy app dashboard --build-memory 4096 --wait
```

Build prepares a resource for use; deploy selects a saved version for production. To pin a version or upgrade an existing deployment:

```sh
vortexify deploy app dashboard --version 3 --wait
vortexify upgrade app dashboard --wait
vortexify undeploy app dashboard
```

Apps, APIs, transforms, agents, connectors, and syncs support deployment. New commits do not automatically replace deployed versions. Deploy upstream resources separately when your app or workflow depends on them.

Building prepares an API for requests. Preparation may complete immediately or continue in the background; use `--wait` to follow the job. Open an app build's preview link to view it in the authenticated dashboard.

For an app hostname, pass a subdomain label:

```sh
vortexify deploy app dashboard --subdomain orders --wait
```

## Configure connectors and secrets

Declare configuration fields in the connector's `config.json`, commit it, then open the configuration wizard:

```sh
vortexify connector shop --configure
vortexify connector shop --check --wait
vortexify connector shop --discover --wait
```

The wizard guides you through fields and uses hidden input for secrets.

To sample a sync without running a full extraction:

```sh
vortexify run sync shop/customers --preview --wait
```

The sync must implement `preview()`. A preview displays sample rows without publishing a table or advancing extraction checkpoints.

Set a project secret for use by APIs and transforms:

```sh
vortexify secret set service_api_key
```

The CLI prompts for the value with hidden input. Reference it from Python with `Secret("service_api_key")`; do not store the value in source files.

## Upload and inspect data

```sh
vortexify upload table ./orders.csv --name orders --wait
vortexify upload file ./guide.pdf --resource-type agent --resource assistant --wait
vortexify tables
vortexify table orders
```

Table uploads support CSV, TSV, and XLSX. Document uploads support PDF, TXT, MD, and Markdown, and attach to an agent or connector. Without an explicit document owner, the CLI prompts you to select one.

`tables` lists available data and readiness. `table NAME` shows details, including available key and relationship findings. Use qualified names or IDs when a short name is ambiguous.

The inventory includes database tables in both preview and origin environments;
use the environment-qualified display name, canonical reference, or ID when names
are ambiguous. Transform outputs and database tables remain listed, with automatic
knowledge graph analysis marked `not applicable`.

Knowledge graph discovery analyzes sync datasets and uploaded tables. Key and relationship findings become available as analysis progresses; a completed analysis can find no meaningful key or relationship.

Use `vortexify table TABLE --verbose` or `--json` to inspect findings and associated jobs, and `vortexify logs JOB_ID` to investigate an analysis failure.

Search attached documents or inspect extracted text:

```sh
vortexify files agent assistant
vortexify files agent assistant --search "shipping rules"
vortexify files preview FILE_ID --chars 2000
```

## Analyze tables with SQL

`analyze` resolves database names in **preview** by default. With `--deployed`,
it resolves them in **origin** and selects deployed file-backed publications.
There is no fallback to origin when a preview table is missing. An explicit
database ID or environment-qualified reference must match the selected mode;
otherwise the CLI reports the conflict before submitting a job.

```sh
vortexify analyze --table reviews=operations.reason_reviews -c 'SELECT count(*) FROM reviews' --wait
vortexify analyze --table reviews=operations.reason_reviews -c 'SELECT count(*) FROM reviews' --deployed --wait
```


Bind a table to a SQL alias and submit a read-only query:

```sh
vortexify analyze --query 'SELECT COUNT(*) FROM orders' --table orders=shop.customers --wait
vortexify analyze --query-file analysis.sql --table orders=shop.customers --wait
```

Result previews show up to **20 rows by default**. Use `--limit` to request between **1 and 100 rows**:

```sh
vortexify analyze --query 'SELECT * FROM orders' --table orders=shop.customers --limit 100 --wait
```

`--limit` controls the result preview, not the number of input rows examined by your SQL query. The same preview limit applies to interactive sessions.

For an interactive SQL session, omit the query:

```sh
vortexify analyze --table orders=shop.customers
```

End a query with `;`, or enter `\g` on its own line to submit. `\q` exits and `\?` shows help. Retrieve a previous result with `vortexify analyze --job JOB_ID`.

## Manage database changes

```sh
vortexify commit --type database --resource operations_db
vortexify database operations_db
vortexify database operations_db --approve 2
# Provision a missing preview and apply its saved schema:
vortexify database operations_db --migrate 2 --environment preview
vortexify database operations_db --reject 2
```

The database view shows pending SQL changes and the latest migration attempt for each environment. Approval and rejection select an explicit saved version and require project admin access. Track the resulting job with `vortexify job JOB_ID --wait`; use `vortexify logs JOB_ID` to investigate a failure before retrying.

## Project context and blueprints

Project context and business knowledge live in Markdown collections created by `init`. Edit those documents with your project code and synchronize them through the CLI:

```sh
vortexify context status
vortexify context pull
vortexify context push
vortexify context read context index.md
```

Find reusable project examples in the blueprint catalog:

```sh
vortexify blueprints search "operations dashboard"
vortexify blueprints attach dashboard
vortexify blueprints list
vortexify blueprints read dashboard
```

`search` starts a blueprint discovery conversation and returns an answer, source citations and a conversation ID. The assistant can refine searches and read design documentation or code. Use `--yaml` for additional recommendation metadata. `list` shows blueprints attached to your project. Continue refining a search explicitly:

```sh
vortexify blueprints search "How does that roster section work?" --conversation <conversation-id>
```

Project discovery similarly investigates accessible projects, apps, committed documents and resource code. Both searches return a `conversation_id`; omit `--conversation` to start fresh. Saved conversations are private to your account and organization; browse previous conversations in the app UI. Describe industry, category and other preferences in the question; the agent can refine them through follow-up questions.

## Install coding-agent skills

Install the [Vortexify skills plugin](https://github.com/vortexifyai/skills) for your coding tool:

```sh
vortexify skills install --agent codex
vortexify skills install --agent claude
```

The selected coding tool must be installed. By default, the CLI looks for `codex` or `claude` on PATH. If your desktop app bundles the CLI without adding it to PATH, supply its executable path explicitly:

```sh
vortexify skills install --agent claude --agent-path "/path/to/bundled/claude"
```

`--agent` still selects the plugin-manager commands; `--agent-path` only selects the executable. Its plugin manager handles installation, updates, and removal. Use `--source GIT_URL` to select another skills repository, or `--dry-run` to print the installation commands without requiring the binary to be installed.

## Help and scripting

Use `--help` on any command for its options:

```sh
vortexify run --help
vortexify connector --help
vortexify deploy --help
```

Commands that support `--json` return structured output for scripts. Use `--org-id` where organization selection would otherwise require a prompt. Inspect `--help` for the flags supported by your installed version.

### Uploaded inputs and recovery

Use `Table("_upload.customers")` in transform/API inputs or agent context after
`vortexify upload table customers.csv --name customers --wait` succeeds.
`_upload` is reserved and cannot name a connector. An explicit upload name is
used exactly or rejected if it already exists; unnamed uploads retain suffixing.
Committing `Table("erp.customers")` or `DatabaseTable("planning.actions")`
requires committed producers, but does not require executing them first.
Execution still requires the referenced data to exist.

`VORTEXIFY_ORG_ID` supplies the default organization; `--org-id` overrides it.

If registration succeeds but starter downloading or verification fails, run
`vortexify create <type> <name> --resume`. This downloads and verifies the saved
source without registering another resource. Existing local folders are preserved.

To recover a context conflict, run `vortexify context pull --backup-local`.
The CLI saves conflicting local files in `.vortexify/backups/*.tar.gz`, then pulls
the server collection. Extract the backup elsewhere, reconcile your edits, and
run `vortexify context push`. Ordinary pull continues to refuse conflicting edits.

Database commits report when migration approval is required. An approval is a
recorded decision, distinct from a successful migration: after a failed migration,
`vortexify database <name> --approve <version>` submits a new job. Inspect the
previous job's logs first. Repeating an upload submission for the same upload ID
reuses active/successful processing; uploading a file again creates a new upload.

### Deploy and run syncs and transforms

`vx deploy sync erp/orders --run --wait` and `vx deploy transform order_readiness --run --wait` execute the selected committed version against production inputs, then activate it after output publication succeeds. Add `--version N` to select a version; `vx upgrade ... --run` selects the latest committed version. Without `--run`, deployment only selects the version.

Development and production outputs and sync checkpoints are separate for each committed version. Deployment never copies preview output or checkpoints. A new incremental sync version starts without a production checkpoint and must support that initial state. Existing production state for the same version is retained.

Use `vx run sync erp/orders --production` or `vx run transform order_readiness --production` for later runs of a deployed version. Production requires a deployed version; ordinary runs use development. Apply required origin database migrations separately before deployment. A failed deployment keeps the previous version selected, but cannot undo writes the run already made to databases or external systems.


### Project-specific blueprint references

```sh
vortexify blueprints metadata budget-planning-hospitals
vortexify blueprints attach budget-planning-hospitals \
  --usage-note "Use the department hierarchy for our planning workflow" \
  --part budget-planning-hospitals-hospital-departments \
  --part-note "budget-planning-hospitals-hospital-departments=Reuse the department navigation"
vortexify blueprints search "Read the design code, then attach the useful department planning parts with a usage note"
vortexify blueprints search "Find suitable scheduling parts for this project" --project-id PROJECT_ID
```

Repeat `--part` to select multiple parts. An omitted usage note or part list preserves
existing attachment context; `--usage-note ""` clears the note. `blueprints list`
returns saved usage notes and selected part IDs with the reference. Project agents
receive the same intent when listing or reading attached designs.

`--usage-note` applies to the blueprint in general. Repeat `--part-note PART_ID=NOTE`
to give selected parts their own instructions. Updating a part note alone preserves
the general note, selected parts, and other part notes. Use `--part-note PART_ID=`
to clear one note. An explicit `--part` list still replaces the selection and removes
notes for deselected parts. Part IDs must belong to the blueprint, and notes require
the corresponding part to be selected. List/read responses expose the notes in
`part_usage_notes`, keyed by canonical part ID.

Blueprint search uses the workspace project when run inside one and remains
organization-scoped outside a workspace. Conversations remain private to your
account and organization; the optional project is context, not conversation ownership.
Without project context, the agent can list accessible projects and read relevant
context/knowledge files to resolve a requested destination. It asks when a project
name is ambiguous. Attaching requires editor access and an explicit request; ordinary
searches do not attach anything. An attachment is a reference, not a built app.


### Links in context and knowledge-base documents

Use relative Markdown links between documents. For registered resources, copy
`resource_reference` from `vortexify registry --json` or `vortexify tables --json`:
`[Plants](vortexify://table/sync/mes/plants)` and
`[Forecast](vortexify://transform/forecast)`. These open the resource/table in the
current project and do not depend on a workspace's local resource paths. Do not
use `vortexify://table/default` or infer links from YAML paths. Default outputs
omit `/default`; named outputs include their encoded output name. Missing or
ambiguous targets show an error. Links do not run resources or pin versions.
The complete convention is in
[document links](https://github.com/vortexifyai/skills/blob/main/skills/vortexify-project/references/document-links.md).


## Guardian readiness

Projects must finish activation before they can run jobs. Check status with `vortexify guardian --json`, or wait with `vortexify guardian --wait`. If activation fails, inspect the error and retry with `vortexify guardian --retry --wait`; retry requires editor access. Once activation succeeds, retry any operation that failed while the project was unready.
