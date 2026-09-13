resource "test" "foo" {
  for_each = ["a"]
  enabled  = true
}
