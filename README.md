# Secret Rotation

A go worker that handles secret rotation for different providers

## Config

```yaml
host: :8080
gcp_project_id: your_project
pubsub_subscription: <id_or_subscription_name> # Can be projects/<project>/subscriptions/<name> or just <name>
handler_label_key: <label_on_secret>
```

The handler label key is used to fetch the right label on the secret to route to the proper handler in the secret-rotation app

## Handlers

### Gandi

Gandi's API provides a rotation endpoint at `https://api.gandi.net/v5/organization/access-tokens` (see the [docs](https://api.gandi.net/docs/organization/#v5-organization-access-tokens))

## Metrics

Endpoint available on `/metrics`

| **name**                           | **type**  | **labels**                     |
| ---------------------------------- | --------- | ------------------------------ |
| `secret_rotation_count`            | COUNTER   | handler, secret_id             |
| `secret_rotation_duration_seconds` | HISTOGRAM | handler, secret_id             |
| `secret_rotation_error_count`      | COUNTER   | handler, secret_id, error_type |
