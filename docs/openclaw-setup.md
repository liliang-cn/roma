# OpenClaw Bridge — Setup Guide

[OpenClaw](https://github.com/openclaw/openclaw) already carries WeChat,
Telegram, iMessage and friends. Its `openclaw mcp serve` bridge exposes every
routed conversation as MCP tools, so TagIt speaks that documented interface
instead of writing one adapter per platform: **one bridge, every channel
OpenClaw supports**.

Note the direction. `tagit mcp` makes TagIt an MCP *server* other agents call.
This is the opposite: TagIt is the MCP *client* of OpenClaw.

---

## Prerequisites

- `tagit` + `tagitd` installed (`brew install liliang-cn/tap/tagit` or `make install`).
- At least one coding agent on `PATH` (e.g. `claude`, `codex`), registered with
  `tagit agent add claude "Claude" $(which claude)`.
- `openclaw` on `PATH`, with its Gateway running and at least one channel
  already paired. Check with `openclaw mcp serve --help`.

## 1. Create `~/.tagit/openclaw.json`

```json
{
  "bindings": []
}
```

That is the whole minimal file. `command` defaults to `openclaw` and `args` to
`["mcp", "serve", "--claude-channel-mode", "off"]`; set them only if the binary
lives somewhere unusual or you want extra flags:

```json
{
  "command": "/opt/homebrew/bin/openclaw",
  "args": ["mcp", "serve", "--claude-channel-mode", "off"],
  "bindings": []
}
```

`--claude-channel-mode off` keeps OpenClaw from also pushing Claude-specific
notifications; TagIt reads the standard `events_wait` / `events_poll` queue.

## 2. Start the daemon

```sh
tagit start
```

The daemon sees the config, spawns `openclaw mcp serve` as a child process and
starts pumping its event queue. Absent config means the bridge is simply off —
the daemon is unaffected either way. Watch it come up:

```sh
tail -f ~/.tagit/tagitd.log | grep openclaw
```

## 3. Find the session key for a conversation

Bindings are keyed by OpenClaw's **session key**, not a channel name. List them:

```sh
openclaw mcp serve   # then call conversations_list from any MCP client
```

Session keys look like:

```
agent:main:telegram:direct:1669479669
agent:main:openclaw-weixin:direct:o9cq…@im.wechat
```

## 4. Bind a conversation to a repo

Message the bot from that conversation, exactly like Feishu/Slack:

```
/bind /path/to/repo      link this conversation to a repo
/agent codex             pick the agent   (also: /mode, /status, /unbind, /help)
```

Then just talk to it:

```
add input validation to the signup handler
```

It acks (**Got it — one sec… 👀**), works in an isolated `git worktree`,
streams progress back into the same conversation, and posts a final summary.

Bindings are persisted back into `~/.tagit/openclaw.json`, so they survive
restarts. You can also pre-seed them by hand:

```json
{
  "bindings": [
    { "chat_id": "agent:main:telegram:direct:1669479669", "repo": "/path/to/repo", "agent": "claude", "mode": "rage" }
  ]
}
```

---

## How it differs from the Feishu/Slack adapters

OpenClaw routes have **no @mention concept and no threads**. Consequences:

- **The binding is the authorization gate.** There is no `@TagIt` to filter on,
  so an unbound conversation is ignored entirely — TagIt does not even ack it.
  Only add a binding for a conversation you want to hand repo access to.
- **Replies go back through the route**, not into a thread. `messages_send`
  uses the channel, recipient, account id and thread id already recorded on the
  session.
- **The bot ignores anything whose `role` is not `user`**, which is what stops
  it reacting to its own replies and looping.

## Limits worth knowing

- OpenClaw's event queue is **live-only**: it starts when the bridge process
  starts and does not replay history. Messages sent while `tagitd` is down are
  not picked up.
- `messages_send` is **text only** and needs an existing conversation route —
  TagIt cannot open a new conversation, only reply in one.
- If the bridge process dies, the daemon logs it and the bridge stays down until
  the next `tagit start`.
