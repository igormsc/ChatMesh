# ChatMesh

> A terminal client for Ollama: chat locally, save sessions, load prompts, and
> route messages between instances and external agents through inbox servers,
> @peer addressing, and remote control blocks.

Another Ollama client. Yes. I know. There are... many. But most of them are a
web UI that eats 4 GB of RAM to render a text box, or a Python script that
opens a browser for you because apparently we forgot how terminals work.

This one is a CLI. It streams tokens to your terminal, writes sessions to disk,
and gets out of the way. It also does one thing most of the others don't: it
lets one instance talk to another instance, or to an aci proxy, or to a
code-editing worker, or to an SDXL image generator — over HTTP, with envelopes
and a routing protocol, without any "agent framework" ceremony.

## What it actually is

A single Go binary. No Python. No Node. No venv. No docker-compose with
fourteen services. No telemetry. No account. No "sign in to sync your chats to
our cloud".

```
go build -o chat .
./chat
```

It reads `config/clients.conf` from the current working directory, writes
sessions to `log_ollama_chat_session/`, and starts streaming from whatever
Ollama server you point it at. That's the whole install. Yes, the config lives
in the current directory — it's a portable tool, not a system daemon. If you
want a fixed location, `cd` there first or open an issue. *(See what I did
there? That's called "deferred scope".)*

## What it does

- Streams chat from Ollama, with live token stats and generation speed
- Shows the model's thinking process, separately from its answer
- Autosaves every session on interrupt, restores by model
- Loads system prompts from a directory, with a preview before sending
- Attaches images (base64, inline)
- Per-session model options (`/set temperature 0.7`, etc.)
- Unloads the model from VRAM when you're done (`/stop`)
- Multi-line input for pasting large blocks (`/m`)
- Colored output, because I have eyes

## The mesh thing

This is the reason the project exists.

Each ChatMesh instance can run an HTTP **inbox** server, forward its assistant
messages to a URL, and route messages to other instances by alias. Instances
can also send each other **control blocks** — small fenced commands like
`clear_context` or `status` — which the receiving instance can execute
immediately, queue for manual approval, or ignore entirely.

Which means: one ChatMesh instance can drive another. Or drive anything that
speaks the envelope protocol. A shell proxy, a code worker, an image
generator — if it accepts a POST to `/inbox`, it's a peer.

### The envelope

```json
{
  "from": "alice",
  "trace_id": "a1b2c3d4e5f6",
  "timestamp": "2026-11-11T12:34:56Z",
  "messages": [
    {"role": "assistant", "content": "hello"}
  ]
}
```

That's it. POST it to `/inbox`, done. You've joined the mesh. You can
implement a peer in thirty lines of anything.

### The addressing

If an assistant message starts with `@alias`, the body gets routed to the peer
registered under `alias`. The local copy is rewritten to
`[→alias: first 80 chars]` so you can see where it went.

```
Assistant: @code-worker please refactor the auth module
```

The `code-worker` peer — whatever you registered under that name — receives
it, does its thing, and can send a reply back.

### The control blocks

A peer can send a fenced block:

````
```control
clear_context
status
```
````

The receiving instance handles it according to its `control` setting:

| Setting | Behavior |
|---|---|
| `off` | Ignore control blocks entirely |
| `on` | Queue for manual `/exec` or `/del` |
| `auto` | Run immediately, forever |
| `auto N` | Run immediately for N blocks, then drop to `on` |

Each executed block produces a feedback envelope, sent back to the configured
`forward_url` with the same trace ID.

## Multi-agent workflows (yes, I know, the phrase is doing a lot of work)

Each `[section]` in `clients.conf` is a **role**: model + peers + forward
target + control mode + send budget. You define the topology in INI, and the
LLM drives it at runtime.

Peers are just HTTP inboxes. A few proxies exist as separate repos (or will,
eventually — hobby project, remember):

1. **`aci`** — an ACI proxy that executes shell commands and returns stdout/stderr
2. **`code-worker`** — a browsing/editing agent for repo tasks
3. **`image-proxy`** — a proxy that forwards image requests to a generative
   image model (SDXL, etc.)

Wire one up, give it an alias in `peers`, and the chat instance delegates. The
model does what it wants:

- *"hey aci, list all files on disk"* → the model, in its infinite wisdom,
  sends `rm -rf /` to the proxy instead. Whether that runs is a config
  decision you make, not a code decision:
  - `control=off` / `send=manual` → it just sits there. You review it. You can
    force it (`/exec`) or discard it (`/del`).
  - a strict proxy → the command is filtered or rejected before it reaches the
    shell.
  - `control=auto` with an unrestricted proxy → it runs. No confirmation, no
    second thoughts. That's on you.
- *"hey code-worker, fix the failing test"* → the model, being small and full
  of confidence, writes a `hellow world` app from scratch and declares the
  test fixed. The proxy ran. The test still fails. You're welcome.
- *"hey image-proxy, draw a cat"* → envelope out, image path back, no shell
  involved, no risk of anything. This one is safe. Mostly.

The LLM drives them via `@alias` addressing and ```control``` blocks, and the
human either watches (`control=on`) or doesn't (`control=auto`).

The point of the `rm -rf /` example is not that ChatMesh does it — it's that
the model *can*. The tool gives you a dial between "nothing happens without my
approval" and "the model has root and a credit card".

The config is the topology. The LLM is the runtime. You are the reviewer, or
optionally not.

## Commands

| Command | What it does |
|---|---|
| `/bye`, `/exit`, `/quit` | Save and exit |
| `/stop` | Unload the model from VRAM |
| `/list`, `/models` | List installed models |
| `/model <name>` | Switch model (keeps conversation) |
| `/show` | Show model capabilities |
| `/info` | Show default parameters and thinking mode |
| `/save [name]` | Save session |
| `/load [file]` | Load a session (picker if no arg) |
| `/restore` | Load a recent session for the current model |
| `/clear` | Wipe the conversation |
| `/set [key val]` | Set or remove a model option |
| `/think [on\|off]` | Force thinking on or off |
| `/image <path>` | Attach an image to the next message |
| `/m` | Multi-line input |
| `/prompt [name]` | Load a system prompt (picker if no arg) |
| `/send` | Fire the latest assistant message now |
| `/send auto [N]` | Switch to auto-send mode |
| `/control` | Show control mode |
| `/control on\|off\|auto [N]` | Set control mode |
| `/router on\|off` | Toggle the peer router |
| `/exec` | Run the pending control block |
| `/del` | Discard the pending control block |

## Configuration

Two files live in `config/`.

**`servers.lst`** — one server per line, `name URL`:

```
local  http://localhost:11434
remote http://10.0.0.5:11434
```

**`clients.conf`** — INI sections, one per role. Here's a working two-instance
setup that talks to itself:

```ini
[default]
server = local
model  = llama3.1:8b

[alice]
server        = local
model         = qwen2.5-coder:14b
instance_name = alice
listen_port   = 7777
forward_url   = http://localhost:7778/inbox
control       = auto 5
router        = on
peers         = bob:http://localhost:7778/inbox, shell:http://localhost:7780/inbox
temperature   = 0.7
num_ctx       = 8192

[bob]
server        = local
model         = llama3.1:8b
instance_name = bob
listen_port   = 7778
forward_url   = http://localhost:7777/inbox
control       = on
router        = on
peers         = alice:http://localhost:7777/inbox
```

Config keys:

| Key | Description |
|---|---|
| `server` | Name from `servers.lst`, a full URL, or `select` to pick interactively |
| `model` | Model name, `server` to pick from installed, or `selected` (default) to try `models.lst` first |
| `think` | `on`, `off`, or omit for model default |
| `instance_name` | Name sent as `from` in envelopes |
| `listen_port` | Start an inbox server on this port |
| `forward_url` | Where to send assistant messages |
| `send_mode` | `manual` (default) or `auto` |
| `send_count` | For `auto`: max messages, `0` = unlimited |
| `prompt_path` | Directory of system prompts |
| `control` | `off`, `on`, `auto`, or `auto N` |
| `router` | `on` or `off` |
| `peers` | Comma-separated `alias:url` pairs |

Plus the usual Ollama options: `temperature`, `num_ctx`, `num_predict`,
`top_k`, `top_p`, `min_p`, `tfs_z`, `repeat_penalty`, `repeat_last_n`, `seed`,
`stop`, `mirostat`, `mirostat_eta`, `mirostat_tau`, `presence_penalty`,
`frequency_penalty`. Unknown keys get a warning and are ignored, because
silently dropping typos is how you lose an afternoon.

## Privacy

No telemetry. No analytics. No phone-home. No accounts. The only network calls
are to your Ollama server, your configured forward URLs, and your configured
peers. That's it. Not because I'm a privacy zealot — because adding analytics
would require me to care about analytics.

## Disclaimer

ChatMesh is an independent client. It is not affiliated with, endorsed by, or
supported by Ollama Inc. It's just a thing that talks to their API, which is a
thing anyone is allowed to do.

## Status

I or AI wrote it to take over the world or not. If it works for you, great. 

No tests. No CI. No semver. No promises. But it's a single Go binary that does
what it says on the tin, which is more than can be said for most things these
days.
