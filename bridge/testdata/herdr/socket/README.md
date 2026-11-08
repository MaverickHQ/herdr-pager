# herdr 0.9.3 socket fixtures (protocol 22)

From herdr 0.9.3's own API schema (in the installed binary, identical to the source at tag v0.9.3, protocol 22), not captured live; the desk checkpoint confirms them.

They were written on 2026-10-22, in phase 2's grilling (item 12), without contacting any herdr server: from the schema, herdr's source at that tag, and phase 0's captures beside this folder. The bridge's herdr client and its fake herdr (`internal/herdrtest`) test against them. Parent design §Bridge components says how the bridge uses each method and maps each error code.

Format: each `.json` file holds exactly one wire line, compact JSON followed by `\n`, as herdr writes it (`serde_json::to_string` plus `\n`, `src/api/server.rs` L957-961). Keys are in **wire order**: `id` then `result`/`error`, `type` first inside `result`, and AgentInfo fields in struct order (`src/api/schema/agents.rs` L186-229). Phase 0's CLI captures have sorted keys instead, because the herdr CLI re-prints responses through `serde_json::Value`. JSON decoders don't care about order. Request files show what the bridge sends: one line per connection.

Every `request*`, `ok*` and `error*` file except the two deliberately invalid requests validates against the API schema embedded in the installed `~/.local/bin/herdr`. That schema is identical, after key sorting, to `docs/next/api/herdr-api.schema.json` at tag v0.9.3 (commit `7b116c05`).

`GH:` = https://github.com/herdrdev/herdr/blob/7b116c05bfda646af39d2524c54e70c751f57ee8/

| File | Provenance | Status |
|---|---|---|
| `agent.list/request.json` | `GH:src/api/schema.rs` L118-119 (`agent.list` takes `EmptyParams`); the schema makes `params` required | shape CONFIRMED |
| `agent.list/ok.json` | Phase 0 `claude/list/blocked-permission.json` with keys put in wire order; envelope per `GH:src/app/api/agents.rs` L36-43 | values CONFIRMED (fixture), order INFERRED (serde) |
| `agent.list/ok-empty.json` | `collect_agent_infos` returns only agent panes (`GH:src/app/agents.rs` L23-37), so a server with no agents gives `[]` | INFERRED |
| `agent.list/ok-unmanaged-inferred.json` | The second agent is **invented**: a `claude` typed into a pane by hand, so it has no `name` (set only by `agent.start` or `agent.rename`) and no `interactive_ready` (true only for managed agents, `GH:src/terminal/state.rs` L2171-2174, and omitted when false). The ids and paths are made up. | INFERRED |
| `agent.list/error-server-unavailable.json` | `GH:src/server/headless.rs` L2830-2843, `GH:src/api/server.rs` L552-560 (exact code and message) | CONFIRMED |
| `agent.get/request.json` | `GH:src/api/schema/common.rs` L54-57 (`AgentTarget {target}`) | CONFIRMED |
| `agent.get/ok.json` | Phase 0 `claude/get/shell-0s.json` with keys put in wire order | values CONFIRMED, order INFERRED |
| `agent.get/error-agent-not-found.json` | `GH:src/app/agents.rs` L293-301 (`agent target {target} not found`); `w1:p9` is an illustrative target | CONFIRMED |
| `agent.read/request.json` | `GH:src/api/schema/agents.rs` L7-17; `source` and `target` are required, `format` defaults to `text`, `lines` is optional | CONFIRMED |
| `agent.read/ok.json` | Envelope and field order per `GH:src/app/api/agents.rs` L218-257 and `GH:src/api/schema/panes.rs` L679-688 (`revision` is hard-coded 0, L252). `text` is phase 0 `claude/detection/shell-0s.txt` byte for byte, which is what `herdr agent read --source detection --format text` printed from `result.read.text` (`GH:src/cli.rs` L86-95). The ids are from `get/shell-0s.json`. The raw line itself was never captured. | shape CONFIRMED, composed line INFERRED |
| `agent.read/error-agent-not-found.json` | `GH:src/app/agents.rs` L293-301 and `GH:src/app/api/agents.rs` L384-390 | CONFIRMED |
| `agent.send_keys/request.json` | `GH:src/api/schema/agents.rs` L19-23; `["1"]` is the approve key from phase 0 `claude/KEYS.md` | CONFIRMED |
| `agent.send_keys/ok.json` | `GH:src/app/api/agents.rs` L372 (`ResponseResult::Ok {}`, so `{"type":"ok"}`) | CONFIRMED |
| `agent.send_keys/error-invalid-key.json` | `GH:src/app/api/agents.rs` L361-366 (`unsupported key {key}`, the first bad key as sent; no bytes are written). `home` is rejected by `parse_key_combo` (`GH:src/config/keybinds.rs` L1281-1339), and herdr-sidebar's own notes record meeting it live. | CONFIRMED |
| `agent.send_keys/error-agent-not-ready.json` | `GH:src/app/api/agents.rs` L346-359 and L376-382. Returned when the pane has no known agent kind, or its foreground job is no longer that agent. | CONFIRMED |
| `agent.send_keys/error-agent-not-found.json` | As for `agent.get` | CONFIRMED |
| `agent.focus/request.json` | `GH:src/api/schema.rs` L134-135 (`AgentTarget`) | CONFIRMED |
| `agent.focus/ok.json` | Envelope `agent_info` with the state after focus (`GH:src/app/agents.rs` L75-88, test `GH:src/app/api/agents.rs` L761-794: done becomes idle). Values from phase 0 `claude/list/after-focus.json`, taken after `herdr agent focus recon`. The CLI's own focus response wasn't saved, so the line is composed. | shape CONFIRMED, composed line INFERRED |
| `agent.focus/error-agent-not-found.json` | As for `agent.get` | CONFIRMED |
| `tab.list/request.json` | Live from herdr 0.9.3 at the desk checkpoint (2026-10-24): `tab.list` with empty `params` answers every tab of every workspace. Allowed since then, read-only, for the tabs' labels (the owner's decision) | shape CONFIRMED (live) |
| `tab.list/ok.json` | Live shape and key order (`TabInfo`: `tab_id`, `workspace_id`, `number`, `label`, `focused`, `pane_count`, `agent_status`; all required in the schema); values illustrative | shape CONFIRMED (live) |
| `workspace.list/request.json` | Live, as `tab.list`: empty `params` | shape CONFIRMED (live) |
| `workspace.list/ok.json` | Live shape and key order (`WorkspaceInfo`: `workspace_id`, `number`, `label`, `focused`, `pane_count`, `tab_count`, `active_tab_id`, `agent_status`); `number` is what herdr's sidebar shows, not the id (`w6` was workspace 5); values illustrative | shape CONFIRMED (live) |
| `envelope/request-unknown-method.json` | A method that doesn't exist in 0.9.3 (`agent.respond`, herdr discussion #4462) | n/a |
| `envelope/error-invalid-request-unknown-method.json` | Code and `invalid request: ` prefix: `GH:src/api/server.rs` L318-345, tests L1380-1402. "unknown variant": `GH:src/api/schema/tests.rs` L418-425. The 102 method names in enum order come from `GH:src/api/schema.rs` L47-252. serde_json may append ` at line 1 column N`. | code CONFIRMED, exact message INFERRED |
| `envelope/request-missing-keys.json` | `agent.send_keys` without `keys` | n/a |
| `envelope/error-invalid-request-missing-field.json` | `GH:src/api/schema/tests.rs` L427-434 (the message names the missing field); the id is echoed (`GH:src/api/server.rs` L1734-1771) | code CONFIRMED, exact message INFERRED (may carry a ` at line 1 column N` suffix) |
| `envelope/request-bad-json.txt` | A truncated request line (not valid JSON, hence `.txt`) | n/a |
| `envelope/error-invalid-request-bad-json.json` | Invalid JSON gives `id:""` (`GH:src/api/server.rs` L318-345, test L1734-1771). The serde_json wording and column are INFERRED. | code and empty id CONFIRMED, message INFERRED |

Tests should match on `error.code` only, never on `error.message`.
