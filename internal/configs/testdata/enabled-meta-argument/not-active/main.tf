resource "test" "foo" {
  enabled = true
}

data "test" "foo" {
  enabled = true
}

ephemeral "test" "foo" {
  enabled = true
}

module "bar" {
  source  = "./bar"
  enabled = true
}
