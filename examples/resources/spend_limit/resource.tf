# Claude Enterprise: a monthly cap of $500.00 (amounts are in cents for USD)
# on one member, overriding what they would inherit from their seat tier,
# group or the organization.
resource "anthropic_spend_limit" "member" {
  scope = {
    type    = "user"
    user_id = "user_01WCz1FkmYMm4gnmykNKUu3Q"
  }
  period = "monthly"
  amount = "50000"
}

# Claude Enterprise: no daily cap for the same member, whatever they would
# inherit. Omitting amount sets an explicit no-limit override for this period
# only; the monthly cap above still applies.
resource "anthropic_spend_limit" "member_daily_unlimited" {
  scope = {
    type    = "user"
    user_id = "user_01WCz1FkmYMm4gnmykNKUu3Q"
  }
  period = "daily"
}

# Claude Console (early access preview): a monthly cap on one workspace.
#
#   resource "anthropic_spend_limit" "workspace" {
#     scope = {
#       type         = "workspace"
#       workspace_id = "wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ"
#     }
#     amount = "100000"
#   }

output "member_spend_limit_id" {
  description = "ID of the member's monthly spend limit."
  value       = anthropic_spend_limit.member.id
}

output "member_spend_limit_currency" {
  description = "Billing currency the amount is expressed in (minor units)."
  value       = anthropic_spend_limit.member.currency
}
