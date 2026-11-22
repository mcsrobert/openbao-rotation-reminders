path "MOUNT/metadata/*" {
  capabilities = ["list", "read"]
}

path "auth/token/revoke-self" {
  capabilities = ["update"]
}
