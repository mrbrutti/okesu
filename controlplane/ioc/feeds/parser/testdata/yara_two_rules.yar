rule One : example
{
    meta:
        author = "test"
        severity = "HIGH"
        description = "first rule"
    strings:
        $a = "hello"
    condition:
        $a
}

rule Two
{
    meta:
        author = "test"
        severity = "MEDIUM"
        description = "second rule"
    strings:
        $b = "world"
    condition:
        $b
}
