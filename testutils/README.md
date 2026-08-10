
## Running it

```bash
go test ./classfile/ -v          # skips the JDK checks if none is present
MOCHA_REQUIRE_JDK=1 go test ./classfile/ -v
```