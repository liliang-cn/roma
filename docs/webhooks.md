# Webhooks

`tagitd` POSTs a JSON notification to your endpoints when a job changes state.

## Configure

`~/.tagit/gateway.json`. Create it, then restart the daemon (`tagit stop && tagit start`).

```json
{
  "endpoints": [
    {
      "id": "ci",
      "target": "https://example.com/tagit-hook",
      "secret": "env:TAGIT_HOOK_SECRET",
      "events": ["task_succeeded", "task_failed"],
      "severity": "low",
      "actions": ["approve", "reject"]
    }
  ]
}
```

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Unique name; appears in delivery events |
| `target` | yes | URL to POST to |
| `secret` | no | Signing key. `env:NAME` reads that environment variable; anything else is the key itself |
| `events` | no | Event types to receive. Omit for all |
| `severity` | no | `low` (default), `medium`, `high`. Anything below the threshold is skipped |
| `sessions` | no | Only these session ids. Omit for all |
| `headers` | no | Extra headers per delivery. Values take `env:NAME` too |
| `actions` | no | Remote commands this endpoint may send back |
| `type` | no | `webhook` (default), `wss`, `telegram` |
| `disabled` | no | `true` keeps the entry but stops delivery |

`headers` is for a receiver that authenticates the sender instead of verifying the signature. The whole value is resolved, so write `"env:RELAY_AUTH"` with `RELAY_AUTH=Bearer xyz`, not `"Bearer env:RELAY_AUTH"`. The headers TagIt sets itself cannot be overridden; naming one fails the load.

A malformed entry disables the whole file and logs why. An endpoint silently dropped is a page silently not sent.

## Events

| Type | Severity |
|---|---|
| `session_started` | low |
| `task_succeeded` | low |
| `task_failed` | high |
| `approval_required` | high |
| `approval_rejected` | medium |
| `task_cancelled` | medium |

## Request

```
POST /your-path
Content-Type: application/json
X-TagIt-Event: task_succeeded
X-TagIt-Notification-Id: notif_job_123_succeeded
X-TagIt-Timestamp: 1788575140
X-TagIt-Signature: sha256=<hex>
```

```json
{
  "id": "notif_job_123_succeeded",
  "type": "task_succeeded",
  "severity": "low",
  "session_id": "sess_123",
  "task_id": "task_123",
  "title": "TagIt task succeeded",
  "summary": "Job job_123 completed with 2 artifact(s).",
  "artifact_refs": ["artifact://art_1", "artifact://art_2"],
  "created_at": "2026-09-05T02:26:42.832Z"
}
```

Any 2xx is success. TagIt retries 429 and 5xx three times with doubling backoff; other 4xx are not retried.

## Verify the signature

The signature covers `<timestamp>.<body>`, so an old capture cannot be replayed against you. Reject anything whose timestamp is outside your own window.

```python
import hmac, hashlib, time

def verify(secret: str, headers, body: bytes, max_age=300) -> bool:
    ts = headers["X-TagIt-Timestamp"]
    if abs(time.time() - int(ts)) > max_age:
        return False
    want = "sha256=" + hmac.new(
        secret.encode(), ts.encode() + b"." + body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(want, headers["X-TagIt-Signature"])
```

```go
func verify(secret, timestamp, signature string, body []byte) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}
```

Without a `secret` no signature header is sent.

## Fan out to chat with hookrelay

[hookrelay](https://github.com/liliang-cn/hookrelay) turns one webhook into Telegram, WeCom, Bark and ntfy. It reads `title` and `summary` out of the envelope directly, so no translation is needed — point an endpoint at it and give it the bearer token:

```json
{
  "endpoints": [
    {
      "id": "hookrelay",
      "target": "http://192.168.123.64:47600/hook",
      "headers": {
        "Authorization": "env:TAGIT_HOOKRELAY_AUTH",
        "X-Hookrelay-Source": "tagit"
      },
      "events": ["task_succeeded", "task_failed", "approval_required"]
    }
  ]
}
```

with the credential in the daemon's environment. hookrelay answers 202 once it has queued the message, which counts as delivered here. Give TagIt its own named token there: the sender label then comes from the credential, and it can be revoked without touching the other senders. The `X-Hookrelay-Source` header is only the fallback for a shared token.

If the daemon reads its environment from a file that a shell sources, **quote any value containing a space**:

```sh
TAGIT_HOOKRELAY_AUTH="Bearer 9f86d081..."   # right
TAGIT_HOOKRELAY_AUTH=Bearer 9f86d081...     # wrong
```

Unquoted, the shell runs the token as a command. The variable ends up empty, every delivery is a 401, and the token lands in the log as `command not found` — treat one written that way as leaked and rotate it.

### macOS: a LAN target needs local-network permission

If the target is a `192.168.x` / `10.x` address and delivery fails with `no route to host` while the same request from a terminal succeeds, this is macOS local-network privacy, not the network. The permission is granted per executable, and a `launchd` job cannot answer the prompt, so it is simply denied. Two ways out: run the daemon from a path that already holds the grant, or give the receiver a public name so it stops being a local-network access at all.

## Audit

Every attempt is an event in the store, delivered or not:

```sh
tagit debug events | grep gateway
```
