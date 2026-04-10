# GPG Key Setup

The release workflow signs the provider checksums with a GPG key. This is required by the [Terraform Registry](https://registry.terraform.io) to verify the authenticity of provider binaries.

## Credentials storage

The GPG key (private key and passphrase) is stored in Ippon's Vaultwarden instance under the **Cloud & DevOps** organization under **Terraform Anthropic provider** folder.

## Generate a GPG key

```bash
gpg --full-generate-key
```

When prompted:

| Prompt | Value |
|--------|-------|
| Key type | `1` (RSA and RSA) |
| Key size | `4096` |
| Expiry | `0` (no expiration) |
| Name / Email | Your identity (visible in the Terraform Registry) |
| Passphrase | A strong passphrase |

## Retrieve the key ID

```bash
gpg --list-secret-keys --keyid-format LONG
```

The key ID is the part after `rsa4096/`:

```
sec   rsa4096/AAAA1111BBBB2222 2024-01-01 [SC]
             ^^^^^^^^^^^^^^^^^
             key ID
```

## Configure GitHub secrets

Add the following secrets to the GitHub repository (`Settings > Secrets and variables > Actions`):

| Secret | Command to get the value |
|--------|--------------------------|
| `GPG_PRIVATE_KEY` | `gpg --armor --export-secret-keys YOUR_KEY_ID` |
| `GPG_PASSPHRASE` | The passphrase chosen during key generation |

## Register the public key in the Terraform Registry

Export the public key:

```bash
gpg --armor --export YOUR_KEY_ID
```

Then paste it in your Terraform Registry namespace:
`registry.terraform.io > Settings > GPG Keys > Add a GPG key`

# GitHub App Setup (Semantic Release)

The semantic-release workflow uses a GitHub App to push the `chore(release):` commit and tags directly to `main`, bypassing the branch ruleset (PR requirement, merge queue, and signed commits).

## Create the App

Go to your GitHub organization (or personal account) → **Settings** → **Developer settings** → **GitHub Apps** → **New GitHub App**

Fill in:

| Field | Value |
|-------|-------|
| Name | e.g. `tf-provider-anthropic-semrel` |
| Homepage URL | Your repository URL (required, unused) |
| Webhooks | Uncheck **Active** |

**Repository permissions** (set to Read & write unless noted):

| Permission | Access |
|------------|--------|
| Contents | Read & write |
| Issues | Read & write |
| Pull requests | Read & write |
| Metadata | Read only (mandatory) |

Click **Create GitHub App**.

## Generate a private key

On the app's settings page, scroll to **Private keys** → **Generate a private key**. A `.pem` file will download — store it securely (e.g. in Vaultwarden under **Terraform Anthropic provider**).

## Note the App ID

The **App ID** (a number) is shown at the top of the app's settings page.

## Install the App on the repository

On the app's settings page → **Install App** → select your org/account → **Only select repositories** → pick this repository → **Install**.

## Configure GitHub secrets

Add the following secrets to the repository (`Settings > Secrets and variables > Actions`):

| Secret                   | Value                                      |
|--------------------------|--------------------------------------------|
| `SEMREL_APP_ID`          | The numeric App ID from the previous step  |
| `SEMREL_APP_PRIVATE_KEY` | The full contents of the `.pem` file       |

## Add the App as a bypass actor in the main branch ruleset

This allows the App to push directly to `main` without going through the PR/merge queue flow and without GPG-signed commits.

1. Go to **Settings** → **Rules** → **Rulesets**
2. Click the ruleset that applies to `main`
3. Scroll to **Bypass list** → click **Add bypass**
4. Select type **GitHub App**, search for and select the app created above
5. Set the bypass mode to **Always**
6. Save
