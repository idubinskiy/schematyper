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

Output package name can be set with `-package`:
```
$ schematyper -package=schema schema.json
```
Defaults to `package main`, with unexported types. Any other package name defaults to exported types.

Name of root type can be set with `-root-type`:
```
$ schematyper -root-type=mySchema schema.json
```

Prefixes for non-root types can be set with `-prefix`:
```
$ schematyper -prefix=my schema.json
```

(`-root-type` and `-prefix` override default exporting rules.)

Can be used with [`go generate`](https://blog.golang.org/generate):
```go
//go:generate schematyper -o schema_type.go -package mypackage schemas/schema.json
```

Print to stdout without outputting to file with `-c`.
```
$ schematyper -c schema.json
```

## Schema Features Support
Supports the following JSON Schema keywords:
* `title` - sets type name
* `description` - sets type comment
* `required` - sets which fields in type don't have `omitempty`
* `properties` - determines struct fields
* `additionalProperties` - determines struct type of map values
* `type` - sets field type (`string`, `bool`, etc.). Examples:
    * `["string", "null"]` sets `*string`
    * `"object"` sets `map[string]interface{}`, `map[string]<new type>`, or a new struct type depending on schema
    * `"array"` sets `[]interface{}` or `[]<new type>` depending on schema
    * `["string", "integer"]` sets `interface{}`
* `items` - sets array items type, similar to `type`
* `format` - if `date-time`, sets type to `time.Time` and imports `time`
* `definitions` - creates additional types which can be referenced using `$ref`
* `$ref` - Reference a local schema (same file).

Support for more features is pending, but many will require adding run-time checks by implementing the `json.Marshaler` and `json.Unmarshaler` interfaces.
