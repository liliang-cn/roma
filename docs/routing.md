# Routing @names to agents

One channel can hold several named teammates. `@dev-agent` runs on one agent, `@qa-agent` on another, and anything unrouted runs on the channel default.

## Set it up

```
@TagIt /bind /path/to/repo
@TagIt /agent claude                  the default for this channel
@TagIt /route @dev-agent codex
@TagIt /route @qa-agent gemini
```

Then use them:

```
@TagIt @dev-agent add input validation to the signup handler
@TagIt @qa-agent run the test suite and report failures
@TagIt just fix the typo in README            → runs on claude, the default
```

The agent id is whatever `tagit agent list` shows, so a route can point at any registered profile.

## Commands

| Command | Effect |
|---|---|
| `/route` | List the routes |
| `/route <@name> <agent-id>` | Add or replace one |
| `/route rm <@name>` | Remove one |
| `/status` | Shows repo, default agent, mode, and routes |

The `@` is optional on input, and names are case-insensitive. On Slack the native slash command works too: `/tagit route @dev-agent codex`.

## How the name is matched

`@name` counts as routing only at the very start of the message. An `@` further in is prose, so `ask @bob about the bug` is a task, not a route.

An unrouted leading name is left in the prompt untouched. `@bob please review` reaches the default agent with `@bob` still in it, because a name you have not routed is much more likely a person than a typo.

You do not need a real bot user per name. You @mention TagIt the way you always did; the second name is ordinary text that TagIt reads. This is why routing works identically on Feishu, Slack, and every channel OpenClaw carries.
