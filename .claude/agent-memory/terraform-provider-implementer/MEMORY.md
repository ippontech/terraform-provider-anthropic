# Memory Index

- [internal/errors providerrors pattern](project_errors_package.md) — Use providerrors.Require* helpers in Configure; never write inline nil checks on pd.Client/pd.AdminClient
- [Two-key provider model](project_api_key_model.md) — Standard resources use pd.Client (ANTHROPIC_API_KEY); admin/org resources use pd.AdminClient (ANTHROPIC_ADMIN_API_KEY)
