# schematyper

Generates Go struct types based on a [JSON Schema](http://json-schema.org/).

## Installation
```
$ go get github.com/idubinskiy/schematyper
```

## Usage
```
$ schematyper schema.json
```
Creates a `schema_schematype.go` file with package `main`.

Output file can be set with `-o`:
```
$ schematyper -o schema_type.go schema.json
```

Output package name can be set with `-p`:
```
$ schematyper -p schema schema.json
```

Can be used with [`go generate`](https://blog.golang.org/generate):
```go
//go:generate schematyper -o schema_type.go -p mypackage schemas/schema.json
```

Print to stdout without outputting to file with `-c`.
```
$ schematyper -c schema.json
```

## Schema Validation Support
Supports the following JSON Schema keywords:
* `title` - sets type name
* `description` - sets type comment
* `required` - sets which fields in type don't have `omitempty`
* `properties` - struct fields
* `type` - sets field type (`string`, `bool`, etc.)
    * `["string", "null"]` sets `*string`
    * `"object"` creates new nested struct type
    * `["string", "integer"]` sets `interface{}`
* `items` - sets slice type (for `array`)
* `format` - if `date-time`, sets type to `time.Time` and imports `time`


**NOTE: `definitions` and `ref` not yet supported.**
