# Vortexify CLI

Build and manage Vortexify projects from your terminal. Create resources, synchronize code, run data workflows, develop React apps, and deploy saved versions.

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

`create` registers a resource and supplies starter code. Edit its files, inspect changes with `status`, and save a new version with `commit`. Use `pull` to retrieve saved code and project documents.

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

Search attached documents or inspect extracted text:

```sh
vortexify files agent assistant
vortexify files agent assistant --search "shipping rules"
vortexify files preview FILE_ID --chars 2000
```

## Analyze tables with SQL

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

The database view shows pending SQL changes and migration jobs. Approval and rejection select an explicit saved version and require project admin access. Track the resulting job with `vortexify job JOB_ID --wait`.

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

`search` starts a new blueprint discovery conversation; the assistant can refine searches and read design documentation or code. Responses can include `recommended_parts` with a part ID, title and explanation of why that section is useful. `list` shows blueprints attached to your project. Continue refining a search explicitly:

```sh
vortexify blueprints search "How does that roster section work?" --conversation <conversation-id>
```

Project discovery similarly investigates accessible projects, apps, committed documents and resource code. Both searches return a `conversation_id`; omit `--conversation` to start fresh. Saved conversations are private to your account and organization; browse previous conversations in the app UI. Blueprint filters (`--industry`, `--subindustry`, `--category`, `--subcategory`, `--module`) are retained for the conversation.

## Install coding-agent skills

Install the [Vortexify skills plugin](https://github.com/vortexifyai/skills) for your coding tool:

```sh
vortexify skills install --agent codex
vortexify skills install --agent claude
```

The selected coding tool must be installed. Its plugin manager handles installation, updates, and removal. Use `--source GIT_URL` to select another skills repository, or `--dry-run` to print the installation commands.

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
