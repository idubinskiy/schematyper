package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gedex/inflector"
)

var (
	needTimeImport bool
	structTypes    []structType
	outToStdout    = flag.Bool("c", false, "output to console; overrides \"-o\"")
	outputFile     = flag.String("o", "", "output file name; default is <schema>_schematype.go")
	packageName    = flag.String("p", "main", "package name for generated file; default is \"main\"")
)

type structField struct {
	Name         string
	Type         string
	Nullable     bool
	PropertyName string
	Required     bool
}

type structType struct {
	Name    string
	Fields  []structField
	Comment string
}

func getTypeString(jsonType, format string) string {
	if format == "date-time" {
		needTimeImport = true
		return "time.Time"
	}

	switch jsonType {
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "null":
		return "nil"
	case "array":
		fallthrough
	case "object":
		return jsonType
	default:
		return "interface{}"
	}
}

// copied from golint (https://github.com/golang/lint/blob/4946cea8b6efd778dc31dc2dbeb919535e1b7529/lint.go#L701)
var commonInitialisms = map[string]bool{
	"API":   true,
	"ASCII": true,
	"CPU":   true,
	"CSS":   true,
	"DNS":   true,
	"EOF":   true,
	"GUID":  true,
	"HTML":  true,
	"HTTP":  true,
	"HTTPS": true,
	"ID":    true,
	"IP":    true,
	"JSON":  true,
	"LHS":   true,
	"QPS":   true,
	"RAM":   true,
	"RHS":   true,
	"RPC":   true,
	"SLA":   true,
	"SMTP":  true,
	"SQL":   true,
	"SSH":   true,
	"TCP":   true,
	"TLS":   true,
	"TTL":   true,
	"UDP":   true,
	"UI":    true,
	"UID":   true,
	"UUID":  true,
	"URI":   true,
	"URL":   true,
	"UTF8":  true,
	"VM":    true,
	"XML":   true,
	"XSRF":  true,
	"XSS":   true,
}

func dashedToWords(s string) string {
	return regexp.MustCompile("-|_").ReplaceAllString(s, " ")
}

func camelCaseToWords(s string) string {
	return regexp.MustCompile(`([\p{Ll}\p{N}])(\p{Lu})`).ReplaceAllString(s, "$1 $2")
}

func generateTypeName(origName string) string {
	spacedName := camelCaseToWords(dashedToWords(origName))
	titledName := strings.Title(spacedName)
	nameParts := strings.Split(titledName, " ")
	for i, part := range nameParts {
		upperedPart := strings.ToUpper(part)
		if commonInitialisms[upperedPart] {
			nameParts[i] = upperedPart
		}
	}
	return strings.Join(nameParts, "")
}

func getType(s *schema, pName, pDesc string) (typeName string) {
	var st structType

	if pName != "" {
		typeName = pName
	} else {
		typeName = s.Title
	}
	if st.Name = generateTypeName(typeName); st.Name == "" {
		log.Fatalln("Can't generate type without name.")
	}

	if pDesc != "" {
		st.Comment = pDesc
	} else {
		st.Comment = s.Description
	}

	required := make(map[string]bool)
	for _, req := range s.Required {
		required[req] = true
	}

	for propName, propSchema := range s.Properties {
		sf := structField{
			PropertyName: propName,
			Required:     required[propName],
		}

		var fieldName string
		if propSchema.Title != "" {
			fieldName = propSchema.Title
		} else {
			fieldName = propName
		}
		if sf.Name = generateTypeName(fieldName); sf.Name == "" {
			log.Fatalln("Can't generate field without name.")
		}

		switch propType := propSchema.Type.(type) {
		case []interface{}:
			if len(propType) == 2 && (propType[0] == "null" || propType[1] == "null") {
				sf.Nullable = true

				jsonType := propType[0]
				if jsonType == "null" {
					jsonType = propType[1]
				}

				sf.Type = getTypeString(jsonType.(string), propSchema.Format)
			}
		case string:
			if propType == "" {
				log.Fatalf("Can't create field %v withput type.", fieldName)
			}
			sf.Type = getTypeString(propType, propSchema.Format)
		case nil:
			log.Fatalf("Can't create field %v without type.", fieldName)
		}
		if sf.Type == "object" {
			sf.Type = getType(propSchema, sf.Name, propSchema.Description)
		} else if sf.Type == "array" {
			switch arrayItemType := propSchema.Items.(type) {
			case []interface{}:
				if len(arrayItemType) == 1 {
					singularName := inflector.Singularize(sf.Name)
					sf.Type = "[]" + getType(getArrayTypeSchema(arrayItemType[0]), singularName, propSchema.Description)
				} else {
					sf.Type = "[]interface{}"
				}
			case interface{}:
				singularName := inflector.Singularize(sf.Name)
				schema := getArrayTypeSchema(arrayItemType)
				sf.Type = "[]" + getType(schema, singularName, propSchema.Description)
			}
		}

		st.Fields = append(st.Fields, sf)
	}

	structTypes = append(structTypes, st)

	return
}

func getArrayTypeSchema(typeInterface interface{}) *schema {
	itemSchemaJSON, _ := json.Marshal(typeInterface)
	var itemSchema schema
	json.Unmarshal(itemSchemaJSON, &itemSchema)
	return &itemSchema
}

func (st structType) print(buf *bytes.Buffer) {
	if st.Comment != "" {
		buf.WriteString(fmt.Sprintln("//", st.Comment))
	}
	buf.WriteString(fmt.Sprintln("type ", st.Name, " struct {"))
	for _, sf := range st.Fields {
		var typeString string
		if sf.Nullable {
			typeString = "*"
		}
		typeString += sf.Type

		tagString := "`json:\"" + sf.PropertyName
		if !sf.Required {
			tagString += ",omitempty"
		}
		tagString += "\"`"
		buf.WriteString(fmt.Sprintln("   ", sf.Name, "  ", typeString, "  ", tagString))
	}
	buf.WriteString("}\n")
}

func main() {
	flag.Parse()

	if flag.NArg() == 0 {
		log.Fatalln("No file to parse.")
	}

	file, err := ioutil.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatalln("Error reading file:", err)
	}

	var s schema
	if err := json.Unmarshal(file, &s); err != nil {
		log.Fatalln("Error parsing JSON:", err)
	}

	schemaName := strings.Split(filepath.Base(flag.Arg(0)), ".")[0]
	getType(&s, schemaName, "")

	var resultSrc bytes.Buffer
	resultSrc.WriteString(fmt.Sprintln("package", *packageName))
	resultSrc.WriteString(fmt.Sprintf("\n// generated by \"%s\" -- DO NOT EDIT\n", strings.Join(os.Args, " ")))
	resultSrc.WriteString("\n")
	if needTimeImport {
		resultSrc.WriteString("import \"time\"\n")
	}
	for _, st := range structTypes {
		st.print(&resultSrc)
		resultSrc.WriteString("\n")
	}
	formattedSrc, err := format.Source(resultSrc.Bytes())
	if err != nil {
		log.Fatalln("Error running gofmt:", err)
	}

	if *outToStdout {
		fmt.Print(string(formattedSrc))
	} else {
		outputFileName := *outputFile
		if outputFileName == "" {
			compactSchemaName := strings.ToLower(generateTypeName(schemaName))
			outputFileName = fmt.Sprintf("%s_schematype.go", compactSchemaName)
		}
		err = ioutil.WriteFile(outputFileName, formattedSrc, 0644)
		if err != nil {
			log.Fatalf("Error writing to %s: %s\n", outputFileName, err)
		}
	}
}
