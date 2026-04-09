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
