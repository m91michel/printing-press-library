# EU Tenders CLI

**The EU public procurement corpus searchable offline, with contactable leads from contract award winners.**

Sync TED notices into a local SQLite store and turn award notices into outreach lists with leads, including email, phone and VAT id per winner when TED reports them. Rank open calls with score and deadline-heat, and analyze markets with concentration, buyer, winner, velocity and cpv-drift. No API key needed.

## Install

The recommended path installs both the `eu-tenders-pp-cli` binary and the `pp-eu-tenders` agent skill (Claude Code, Codex, Cursor, Gemini CLI, GitHub Copilot, and other agents supported by the upstream [`skills`](https://github.com/vercel-labs/skills) CLI) in one shot:

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders
```

For CLI only (no skill):

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders --cli-only
```

For skill only — installs the skill into the same agents as the default command above, but skips the CLI binary (use this to update or reinstall just the skill):

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders --skill-only
```

To constrain the skill install to one or more specific agents (repeatable — agent names match the [`skills`](https://github.com/vercel-labs/skills) CLI):

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders --agent claude-code
npx -y @mvanhorn/printing-press-library install eu-tenders --agent claude-code --agent codex
```

### Without Node (Go fallback)

If `npx` isn't available (no Node, offline), install the CLI directly via Go (requires Go 1.26.6 or newer):

```bash
go install github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/cmd/eu-tenders-pp-cli@latest
```

This installs the CLI only — no skill.

### Pre-built binary

Download a pre-built binary for your platform from the [latest release](https://github.com/mvanhorn/printing-press-library/releases/tag/eu-tenders-current). On macOS, clear the Gatekeeper quarantine: `xattr -d com.apple.quarantine <binary>`. On Unix, mark it executable: `chmod +x <binary>`.

<!-- pp-hermes-install-anchor -->
## Install for Hermes

Install the CLI binary first. The installer writes binaries to a per-user managed bin directory by default: `$HOME/.local/bin` on macOS/Linux and `%LOCALAPPDATA%\Programs\PrintingPress\bin` on Windows.

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders --cli-only
```

Then install the focused Hermes skill.

From the Hermes CLI:

```bash
hermes skills install mvanhorn/printing-press-library/cli-skills/pp-eu-tenders --force
```

Inside a Hermes chat session:

```bash
/skills install mvanhorn/printing-press-library/cli-skills/pp-eu-tenders --force
```

Restart the Hermes session or gateway if the newly installed skill is not visible immediately.

## Install for OpenClaw
Install both the CLI binary and the focused OpenClaw skill. The installer defaults binaries to a per-user bin directory (`$HOME/.local/bin` on macOS/Linux, `%LOCALAPPDATA%\Programs\PrintingPress\bin` on Windows):

```bash
npx -y @mvanhorn/printing-press-library install eu-tenders --agent openclaw
```

Restart the OpenClaw session or gateway if the newly installed skill is not visible immediately.

## Use with Claude Desktop

This CLI ships an [MCPB](https://github.com/modelcontextprotocol/mcpb) bundle — Claude Desktop's standard format for one-click MCP extension installs (no JSON config required).

To install:

1. Download the `.mcpb` for your platform from the [latest release](https://github.com/mvanhorn/printing-press-library/releases/tag/eu-tenders-current).
2. Double-click the `.mcpb` file. Claude Desktop opens and walks you through the install.

Requires Claude Desktop 1.0.0 or later. Pre-built bundles ship for macOS Apple Silicon (`darwin-arm64`) and Windows (`amd64`, `arm64`); for other platforms, use the manual config below.

<details>
<summary>Manual JSON config (advanced)</summary>

If you can't use the MCPB bundle (older Claude Desktop, unsupported platform), install the MCP binary and configure it manually.


```bash
go install github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/cmd/eu-tenders-pp-mcp@latest
```

Add to your Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "eu-tenders": {
      "command": "eu-tenders-pp-mcp"
    }
  }
}
```

</details>

## Quick Start

```bash
# Check config, paths and TED API reachability
eu-tenders-pp-cli doctor

# Pull the last 30 days of German construction notices into the local store
eu-tenders-pp-cli sync --since 30d --param country=DEU --param cpv=45

# List companies that just won construction contracts, with contact data
eu-tenders-pp-cli leads --country DEU --cpv 45 --days 30

# Rank open tenders worth bidding on
eu-tenders-pp-cli score --country DEU --cpv 45 --keywords Neubau

```

## Unique Features

These capabilities aren't available in any other tool for this API.

### Lead generation from award winners
- **`leads`** — Get a contactable outreach list of companies that just won construction contracts: one row per winner with email, phone, city and VAT/HRB id when TED reports them, plus contract value and what is being built.

  _Reach for this when an agent needs sales leads from public contract awards instead of raw notices._

  ```bash
  eu-tenders-pp-cli leads --country DEU --cpv 45 --days 30 --region DE2 --agent
  ```
- **`winner`** — Profile one company that wins public contracts: win timeline, total value, buyers, regions and latest contact data.

  _Use it before calling a lead or sizing up a competitor._

  ```bash
  eu-tenders-pp-cli winner "Johann Bunte" --country DEU --agent
  ```

### Bid prioritization
- **`score`** — Rank open tenders by deadline urgency, contract value and keyword fit into a prioritized bid shortlist.

  _Use it when asked which open tenders are worth bidding on this week._

  ```bash
  eu-tenders-pp-cli score --country DEU --cpv 45 --keywords Hochbau,Neubau --agent
  ```
- **`incumbents`** — For one open tender, see who previously won contracts from the same buyer in the same category.

  _Use it to judge your odds against the incumbent before writing a proposal._

  ```bash
  eu-tenders-pp-cli incumbents 679227-2026 --agent
  ```
- **`deadline-heat`** — A ranked list of tenders closing within days, weighted by urgency, value and how few companies usually win this buyer's awards.

  _Use it for last-minute bid triage._

  ```bash
  eu-tenders-pp-cli deadline-heat --country DEU --cpv 45 --days 14 --agent
  ```

### Market and buyer intelligence
- **`buyer`** — Profile a contracting authority: publishing cadence, CPV mix, typical contract values and repeat winners.

  _Use it before bidding to one authority or when investigating its award history._

  ```bash
  eu-tenders-pp-cli buyer --name "Stadt München" --show-winners --agent
  ```
- **`concentration`** — See which companies capture what share of awarded value in a sector and country, with an HHI concentration score.

  _Use it to answer who dominates a procurement market._

  ```bash
  eu-tenders-pp-cli concentration --country POL --cpv 4523 --top 5 --agent
  ```
- **`win-rate`** — See per buyer how many calls for tender end in published awards and how many distinct companies win.

  _Use it to compare buyers by openness before targeting them._

  ```bash
  eu-tenders-pp-cli win-rate --country DEU --cpv 45 --min-calls 3 --agent
  ```
- **`dark-buyers`** — Flag contracting authorities whose tenders rarely produce public awards or keep going to one company.

  _Use it for procurement integrity screening; results are heuristic signals, not findings._

  ```bash
  eu-tenders-pp-cli dark-buyers --country POL --cpv 45 --agent
  ```

### Trends
- **`velocity`** — See whether a procurement market is heating up or cooling off with weekly notice counts versus the same window last year.

  _Use it to spot demand shifts in a sector._

  ```bash
  eu-tenders-pp-cli velocity --country DEU --cpv 45 --window 90d --compare 1y --agent
  ```
- **`cpv-drift`** — See which procurement categories grow or shrink year over year in a country's spending mix.

  _Use it for market sizing and policy research._

  ```bash
  eu-tenders-pp-cli cpv-drift --country FRA --metric value --top 20 --agent
  ```

## Recipes

### Weekly construction-award leads

```bash
eu-tenders-pp-cli leads --country DEU --cpv 45 --days 7 --new-only --agent --select winner_name,winner_email,winner_phone,winner_city,contract_value,title
```

Only companies not returned by earlier --new-only runs; this run's companies are recorded in the local lead_seen table.

### Group leads by company

```bash
eu-tenders-pp-cli leads --country DEU --cpv 45 --days 90 --group-by company --agent
```

One row per company with win count and total value, ordered by biggest winners.

### Who dominates road construction

```bash
eu-tenders-pp-cli concentration --country POL --cpv 4523 --top 10 --agent
```

Winner share and HHI for a market slice.

### Check the incumbent before bidding

```bash
eu-tenders-pp-cli incumbents 679227-2026 --agent
```

Prior winners from the same buyer and category.

## Usage

Run `eu-tenders-pp-cli --help` for the full command reference and flag list.

## Paths & environment variables

This CLI separates local files into four path kinds:

| Kind | Contents |
|------|----------|
| `config` | User-editable settings such as `config.toml` and saved profiles |
| `data` | Durable local data such as `data.db` |
| `state` | Runtime state such as `teach.log` |
| `cache` | Regenerable HTTP/cache files |

Each kind resolves independently. The ladder is:

1. Per-kind env var: `EU_TENDERS_CONFIG_DIR`, `EU_TENDERS_DATA_DIR`, `EU_TENDERS_STATE_DIR`, or `EU_TENDERS_CACHE_DIR`
2. `--home <dir>` for this invocation
3. `EU_TENDERS_HOME` for a flat relocated root
4. XDG env vars: `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`
5. Platform defaults matching existing installs

For containers and agent sandboxes, prefer a single relocated root:

```bash
export EU_TENDERS_HOME=/srv/eu-tenders
eu-tenders-pp-cli doctor
```

Under `EU_TENDERS_HOME=/srv/eu-tenders`, the four dirs resolve to `/srv/eu-tenders/config`, `/srv/eu-tenders/data`, `/srv/eu-tenders/state`, and `/srv/eu-tenders/cache`.

MCP servers do not receive CLI flags from the host. Put relocation in the host `env` block:

```json
{
  "mcpServers": {
    "eu-tenders": {
      "command": "eu-tenders-pp-mcp",
      "env": {
        "EU_TENDERS_HOME": "/srv/eu-tenders"
      }
    }
  }
}
```

Precedence matters in fleets: an ambient per-kind variable such as `EU_TENDERS_DATA_DIR` overrides an explicit `--home` for that kind. Use `EU_TENDERS_HOME` or the per-kind variables for durable fleet relocation; treat `--home` as the weaker per-invocation lever.

Relocation is one-way. Unsetting `EU_TENDERS_HOME` does not move files back to platform defaults, and `doctor` cannot find files left under a former root. Move the files manually before unsetting relocation variables.

Existing installs keep working because the platform-default rung matches the legacy layout. Run `eu-tenders-pp-cli doctor --fail-on warn` to check path warnings in automation.

## Commands

### Search and lookup (live TED API)
- **`eu-tenders-pp-cli notices --query "buyer-country=DEU AND classification-cpv=45000000"`** - Expert-query search over TED notices (`--fields`, `--limit`, `--page`, `--pagination-mode`, `--scope`)
- **`eu-tenders-pp-cli notices get 680471-2026`** - One notice by publication number, with winners and their contacts
- **`eu-tenders-pp-cli awards --country DEU --cpv 45`** - Contract award notices with winner, buyer and value (`--winner`, `--year`, `--since`)
- **`eu-tenders-pp-cli deadline --country DEU --days 14`** - Open calls for tender whose submission deadline is within N days
- **`eu-tenders-pp-cli cpv get 45500000`** / **`cpv search`** (keyword argument, e.g. machinery) - CPV code lookup
- **`eu-tenders-pp-cli fields --contains winner`** - TED expert-query field names

### Local store (offline)
- **`eu-tenders-pp-cli sync --country DEU --cpv 45 --since 90d`** - Sync calls for tender and awards into SQLite (tables `notices`, `notice_winners`, `notices_fts`; `--type`, `--param key=value`, `--until`, `--limit`, `--max-pages`, `--db`)
- **`eu-tenders-pp-cli search "Neubau Schule"`** - Full-text search over synced titles, buyers and winners
- **`eu-tenders-pp-cli sql "SELECT buyer_country, COUNT(*) FROM notices GROUP BY 1"`** - Read-only SELECT against the store


### Self-learning loop

This CLI caches per-question discovery so repeat queries skip the walk and structurally similar queries get answered via entity substitution. The loop also self-captures: every invocation is journaled locally, and failed-flag corrections plus fresh teaches surface as candidates on the next `recall` for confirm/reject judgment. Agents call `recall` before discovery and fire `teach &` after answering. See the `## Automatic learning` section in `SKILL.md` for the full protocol.

- **`eu-tenders-pp-cli recall <query>`** - Look up cached resources for a query before running discovery
- **`eu-tenders-pp-cli teach`** - Record a query -> resource mapping (silent on success, safe to background with `&`)
- **`eu-tenders-pp-cli learnings list`** - Inspect taught rows
- **`eu-tenders-pp-cli learnings forget <query>`** - Undo a teach
- **`eu-tenders-pp-cli learnings candidates`** - List auto-captured candidates awaiting confirm/reject
- **`eu-tenders-pp-cli learnings stats`** - Local loop metrics: recall hit rate, teach-to-reuse, playbook resolution, candidate counts
- **`eu-tenders-pp-cli teach-pattern`** - Install a query/resource template up front
- **`eu-tenders-pp-cli teach-lookup`** - Add an entity mapping (e.g. country code, team alias) for pattern substitution

Pass `--no-learn` or set `EU_TENDERS_NO_LEARN=true` to disable the loop for deterministic flows.

The local store's schema version stamp is one-way: once this version of `eu-tenders-pp-cli` opens the database, older binaries refuse it with a version error — upgrade the binary rather than downgrading.

## Output Formats

```bash
# Human-readable table (default in terminal, JSON when piped)
eu-tenders-pp-cli leads --country DEU --cpv 45

# JSON for scripting and agents
eu-tenders-pp-cli leads --country DEU --cpv 45 --json
# Filter to specific fields by name
eu-tenders-pp-cli leads --country DEU --cpv 45 --json --select winner_name,winner_email,contract_value

# Dry run — show the request without sending
eu-tenders-pp-cli notices --query "buyer-country=DEU" --dry-run

# Agent mode — JSON + compact + no prompts in one flag
eu-tenders-pp-cli leads --country DEU --cpv 45 --agent
```

## Agent Usage

This CLI is designed for AI agent consumption:

- **Non-interactive** - never prompts, every input is a flag
- **Pipeable** - `--json` output to stdout, errors to stderr
- **Filterable** - `--select <field>[,<field>...]` returns only fields you need
- **Previewable** - `--dry-run` shows the request without sending
- **Read-only by default** - this CLI does not create, update, delete, publish, send, or mutate remote resources
- **Local writes** - Local writes are limited to the SQLite store (`sync`, `leads --new-only`), the learn loop and saved profiles.
- **Agent-safe by default** - no colors or formatting unless `--human-friendly` is set

Exit codes: `0` success, `1` other error, `2` usage error, `3` not found, `4` access refused by TED (HTTP 401/403 or HTML block page), `5` API error, `7` rate limited, `10` config error.

## Health Check

```bash
eu-tenders-pp-cli doctor
```

Verifies configuration and connectivity to the API.

## Configuration

Run `eu-tenders-pp-cli doctor` to see the resolved config, data, state, and cache directories. The platform-default config path is `~/.config/eu-tenders-pp-cli/config.toml`; `--home`, `EU_TENDERS_HOME`, and per-kind env vars can relocate it.

Static request headers can be configured under `headers`; per-command header overrides take precedence.

## Troubleshooting
**Not found errors (exit code 3)**
- Check the publication number format (e.g. `680471-2026`) for `notices get` and `incumbents`
- For `cpv get`, find valid codes with `eu-tenders-pp-cli cpv search <keyword>`

### API-specific
- **Analytics commands return no rows** — Run eu-tenders-pp-cli sync --since 90d --param country=DEU --param cpv=45 first; they read the local store
- **Lead titles look like reference codes** — Re-run eu-tenders-pp-cli sync for that window; sync stores the procedure title (title-proc) ahead of lot reference titles
- **Query syntax error from TED** — Run eu-tenders-pp-cli fields to check field names; values with spaces need quotes
