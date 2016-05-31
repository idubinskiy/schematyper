package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/gedex/inflector"
)

//go:generate schematyper --root-type=metaSchema --prefix=meta metaschema.json

var (
	outToStdout     = kingpin.Flag("console", "output to console instead of file").Default("false").Short('c').Bool()
	outputFile      = kingpin.Flag("out-file", "filename for output; default is <schema>_schematype.go").Short('o').String()
	packageName     = kingpin.Flag("package", `package name for generated file; default is "main"`).Default("main").String()
	rootTypeName    = kingpin.Flag("root-type", `name of root type; default is generated from the filename`).String()
	typeNamesPrefix = kingpin.Flag("prefix", `prefix for non-root types`).String()
	inputFile       = kingpin.Arg("input", "file containing a valid JSON schema").Required().ExistingFile()
)

type structField struct {
	Name         string
	TypeRef      string
	TypePrefix   string
	Nullable     bool
	PropertyName string
	Required     bool
}

type structFields []structField

func (s structFields) Len() int {
	return len(s)
}

func (s structFields) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

func (s structFields) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

type goType struct {
	Name       string
	TypeRef    string
	TypePrefix string
	Nullable   bool
	Fields     structFields
	Comment    string

	parentPath   string
	origTypeName string
}

func (gt goType) print(buf *bytes.Buffer) {
	if gt.Comment != "" {
		buf.WriteString(fmt.Sprintf("// %s\n", gt.Comment))
	}
	typeStr := gt.TypePrefix
	baseType, ok := types[gt.TypeRef]
	if ok {
		typeStr += baseType.Name
	}
	buf.WriteString(fmt.Sprintf("type %s %s", gt.Name, typeStr))
	if typeStr != "struct" {
		buf.WriteString("\n")
		return
	}
	buf.WriteString(" {\n")
	sort.Stable(gt.Fields)
	for _, sf := range gt.Fields {
		sfTypeStr := sf.TypePrefix
		sfBaseType, ok := types[sf.TypeRef]
		if ok {
			sfTypeStr += sfBaseType.Name
		}
		if sf.Nullable && sfTypeStr != "interface{}" {
			sfTypeStr = "*" + sfTypeStr
		}

		tagString := "`json:\"" + sf.PropertyName
		if !sf.Required {
			tagString += ",omitempty"
		}
		tagString += "\"`"
		buf.WriteString(fmt.Sprintf("%s %s %s\n", sf.Name, sfTypeStr, tagString))
	}
	buf.WriteString("}\n")
}

type goTypes []goType

func (t goTypes) Len() int {
	return len(t)
}

func (t goTypes) Less(i, j int) bool {
	return t[i].Name < t[j].Name
}

func (t goTypes) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

var needTimeImport bool

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

type stringSet map[string]struct{}

func newStringSet(vals ...string) stringSet {
	s := make(stringSet)
	for _, val := range vals {
		s[val] = struct{}{}
	}
	return s
}

func stringSetFromMapKeys(vals interface{}) (stringSet, error) {
	v := reflect.ValueOf(vals)
	if v.Kind() != reflect.Map {
		return nil, errors.New("not a map")
	}
	if v.Type().Key().Kind() != reflect.String {
		return nil, errors.New("map values are not type string")
	}

	s := make(stringSet)
	for _, val := range v.MapKeys() {
		s[val.Interface().(string)] = struct{}{}
	}
	return s, nil
}

func (s stringSet) add(val string) {
	s[val] = struct{}{}
}

func (s stringSet) remove(val string) {
	delete(s, val)
}

func (s stringSet) exists(val string) bool {
	_, ok := s[val]
	return ok
}

func (s stringSet) equals(t stringSet) bool {
	if len(s) != len(t) {
		return false
	}

	for val := range s {
		if _, ok := t[val]; !ok {
			return false
		}
	}

	for val := range t {
		if _, ok := s[val]; !ok {
			return false
		}
	}

	return true
}

func (s stringSet) String() string {
	b := bytes.Buffer{}
	b.WriteRune('(')

	for val := range s {
		b.WriteString(val)
		b.WriteRune(' ')
	}

	if size := b.Len(); size > 1 {
		b.Truncate(size - 1)
	}

	b.WriteRune(')')

	return b.String()
}

// copied from golint (https://github.com/golang/lint/blob/4946cea8b6efd778dc31dc2dbeb919535e1b7529/lint.go#L701)
var commonInitialisms = newStringSet(
	"API",
	"ASCII",
	"CPU",
	"CSS",
	"DNS",
	"EOF",
	"GUID",
	"HTML",
	"HTTP",
	"HTTPS",
	"ID",
	"IP",
	"JSON",
	"LHS",
	"QPS",
	"RAM",
	"RHS",
	"RPC",
	"SLA",
	"SMTP",
	"SQL",
	"SSH",
	"TCP",
	"TLS",
	"TTL",
	"UDP",
	"UI",
	"UID",
	"UUID",
	"URI",
	"URL",
	"UTF8",
	"VM",
	"XML",
	"XSRF",
	"XSS",
)

func dashedToWords(s string) string {
	return regexp.MustCompile("-|_").ReplaceAllString(s, " ")
}

func camelCaseToWords(s string) string {
	return regexp.MustCompile(`([\p{Ll}\p{N}])(\p{Lu})`).ReplaceAllString(s, "$1 $2")
}

func getExportedIdentifierPart(part string) string {
	upperedPart := strings.ToUpper(part)
	if commonInitialisms.exists(upperedPart) {
		return upperedPart
	}
	return strings.Title(strings.ToLower(part))
}

func generateIdentifier(origName string, exported bool) string {
	spacedName := camelCaseToWords(dashedToWords(origName))
	titledName := strings.Title(spacedName)
	nameParts := strings.Split(titledName, " ")
	for i, part := range nameParts {
		nameParts[i] = getExportedIdentifierPart(part)
	}
	if !exported {
		nameParts[0] = strings.ToLower(nameParts[0])
	}
	rawName := strings.Join(nameParts, "")

	// make sure we build a valid identifier
	buf := &bytes.Buffer{}
	for pos, char := range rawName {
		if unicode.IsLetter(char) || char == '_' || (unicode.IsDigit(char) && pos > 0) {
			buf.WriteRune(char)
		}
	}

	return buf.String()
}

func generateTypeName(origName string) string {
	if *packageName != "main" || *typeNamesPrefix != "" {
		return *typeNamesPrefix + generateIdentifier(origName, true)
	}

	return generateIdentifier(origName, false)
}

func generateFieldName(origName string) string {
	return generateIdentifier(origName, true)
}

func getTypeSchema(typeInterface interface{}) *metaSchema {
	typeSchemaJSON, _ := json.Marshal(typeInterface)
	var typeSchema metaSchema
	json.Unmarshal(typeSchemaJSON, &typeSchema)
	return &typeSchema
}

func getTypeSchemas(typeInterface interface{}) map[string]*metaSchema {
	typeSchemasJSON, _ := json.Marshal(typeInterface)
	var typeSchemas map[string]*metaSchema
	json.Unmarshal(typeSchemasJSON, &typeSchemas)
	return typeSchemas
}

func singularize(plural string) string {
	singular := inflector.Singularize(plural)
	if singular == plural {
		singular += "Item"
	}
	return singular
}

func parseAdditionalProperties(ap interface{}) (hasAddl bool, addlSchema *metaSchema) {
	switch ap := ap.(type) {
	case bool:
		return ap, nil
	case map[string]interface{}:
		return true, getTypeSchema(ap)
	default:
		return
	}
}

type deferredType struct {
	schema     *metaSchema
	name       string
	desc       string
	parentPath string
}

type stringSetMap map[string]stringSet

func (m stringSetMap) addTo(set, val string) {
	if m[set] == nil {
		m[set] = newStringSet()
	}
	m[set].add(val)
}

func (m stringSetMap) removeFrom(set, val string) {
	if m[set] == nil {
		return
	}
	m[set].remove(val)
}

func (m stringSetMap) existsIn(set, val string) bool {
	if m[set] == nil {
		return false
	}
	return m[set].exists(val)
}

func (m stringSetMap) delete(set string) {
	delete(m, set)
}

func (m stringSetMap) has(set string) bool {
	_, ok := m[set]
	return ok
}

var types = make(map[string]goType)
var deferredTypes = make(map[string]deferredType)
var typesByName = make(stringSetMap)

func processType(s *metaSchema, pName, pDesc, path, parentPath string) (typeRef string) {
	if len(s.Definitions) > 0 {
		parseDefs(s, path)
	}

	var gt goType

	// avoid 'recursive type' problem, at least for the root type
	if path == "#" {
		gt.Nullable = true
	}

	if s.Ref != "" {
		if _, ok := types[s.Ref]; ok {
			return s.Ref
		}
		deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
		return ""
	}

	gt.parentPath = parentPath

	if path == "#" {
		gt.origTypeName = *rootTypeName
		gt.Name = *rootTypeName
	} else {
		gt.origTypeName = s.Title
		if gt.origTypeName == "" {
			gt.origTypeName = pName
		}

		if gt.Name = generateTypeName(gt.origTypeName); gt.Name == "" {
			log.Fatalln("Can't generate type without name.")
		}
	}

	typeRef = path

	gt.Comment = s.Description
	if gt.Comment == "" {
		gt.Comment = pDesc
	}

	required := newStringSet()
	for _, req := range s.Required {
		required.add(string(req))
	}

	defer func() {
		types[path] = gt
		typesByName.addTo(gt.Name, path)
	}()

	var jsonType string
	switch schemaType := s.Type.(type) {
	case []interface{}:
		if len(schemaType) == 2 && (schemaType[0] == "null" || schemaType[1] == "null") {
			gt.Nullable = true

			jsonType = schemaType[0].(string)
			if jsonType == "null" {
				jsonType = schemaType[1].(string)
			}
		}
	case string:
		jsonType = schemaType
	}

	props := getTypeSchemas(s.Properties)
	hasProps := len(props) > 0
	hasAddlProps, addlPropsSchema := parseAdditionalProperties(s.AdditionalProperties)

	typeString := getTypeString(jsonType, s.Format)
	switch typeString {
	case "object":
		if gt.Name == "Properties" {
			panic(fmt.Errorf("props: %+v\naddlPropsSchema: %+v\n", props, addlPropsSchema))
		}
		if hasProps && !hasAddlProps {
			gt.TypePrefix = "struct"
		} else if !hasProps && hasAddlProps && addlPropsSchema != nil {
			singularName := singularize(gt.origTypeName)
			gotType := processType(addlPropsSchema, singularName, s.Description, path+"/additionalProperties", path)
			if gotType == "" {
				deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
				return ""
			}
			gt.TypePrefix = "map[string]"
			gt.TypeRef = gotType
		} else {
			gt.TypePrefix = "map[string]interface{}"
		}
	case "array":
		switch arrayItemType := s.Items.(type) {
		case []interface{}:
			if len(arrayItemType) == 1 {
				singularName := singularize(gt.origTypeName)
				typeSchema := getTypeSchema(arrayItemType[0])
				gotType := processType(typeSchema, singularName, s.Description, path+"/items/0", path)
				if gotType == "" {
					deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
					return ""
				}
				gt.TypePrefix = "[]"
				gt.TypeRef = gotType
			} else {
				gt.TypePrefix = "[]interface{}"
			}
		case interface{}:
			singularName := singularize(gt.origTypeName)
			typeSchema := getTypeSchema(arrayItemType)
			gotType := processType(typeSchema, singularName, s.Description, path+"/items", path)
			if gotType == "" {
				deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
				return ""
			}
			gt.TypePrefix = "[]"
			gt.TypeRef = gotType
		default:
			gt.TypePrefix = "[]interface{}"
		}
	default:
		gt.TypePrefix = typeString
	}

	for propName, propSchema := range props {
		sf := structField{
			PropertyName: propName,
			Required:     required.exists(propName),
		}

		var fieldName string
		if propSchema.Title != "" {
			fieldName = propSchema.Title
		} else {
			fieldName = propName
		}
		if sf.Name = generateFieldName(fieldName); sf.Name == "" {
			log.Fatalln("Can't generate field without name.")
		}

		if propSchema.Ref != "" {
			if refType, ok := types[propSchema.Ref]; ok {
				sf.TypeRef, sf.Nullable = propSchema.Ref, refType.Nullable
				gt.Fields = append(gt.Fields, sf)
				continue
			}
			deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
			return ""
		}

		switch propType := propSchema.Type.(type) {
		case []interface{}:
			if len(propType) == 2 && (propType[0] == "null" || propType[1] == "null") {
				sf.Nullable = true

				jsonType := propType[0]
				if jsonType == "null" {
					jsonType = propType[1]
				}

				sf.TypePrefix = getTypeString(jsonType.(string), propSchema.Format)
			}
		case string:
			sf.TypePrefix = getTypeString(propType, propSchema.Format)
		case nil:
			sf.TypePrefix = "interface{}"
		}

		refPath := path + "/properties/" + propName

		props := getTypeSchemas(propSchema.Properties)
		hasProps := len(props) > 0
		hasAddlProps, addlPropsSchema := parseAdditionalProperties(propSchema.AdditionalProperties)

		if sf.TypePrefix == "object" {
			if hasProps && !hasAddlProps {
				sf.TypeRef = processType(propSchema, sf.Name, propSchema.Description, refPath, path)
			} else if !hasProps && hasAddlProps && addlPropsSchema != nil {
				singularName := singularize(propName)
				gotType := processType(addlPropsSchema, singularName, propSchema.Description, refPath+"/additionalProperties", path)
				if gotType == "" {
					deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
					return ""
				}
				sf.TypePrefix = "map[string]"
				sf.TypeRef = gotType
			} else {
				sf.TypePrefix = "map[string]interface{}"
			}
		} else if sf.TypePrefix == "array" {
			switch arrayItemType := propSchema.Items.(type) {
			case []interface{}:
				if len(arrayItemType) == 1 {
					singularName := singularize(propName)
					typeSchema := getTypeSchema(arrayItemType[0])
					gotType := processType(typeSchema, singularName, propSchema.Description, refPath+"/items/0", path)
					if gotType == "" {
						deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
						return ""
					}
					sf.TypePrefix = "[]"
					sf.TypeRef = gotType
				} else {
					sf.TypePrefix = "[]interface{}"
				}
			case interface{}:
				singularName := singularize(propName)
				typeSchema := getTypeSchema(arrayItemType)
				gotType := processType(typeSchema, singularName, propSchema.Description, refPath+"/items", path)
				if gotType == "" {
					deferredTypes[path] = deferredType{schema: s, name: pName, desc: pDesc, parentPath: parentPath}
					return ""
				}
				sf.TypePrefix = "[]"
				sf.TypeRef = gotType
			default:
				sf.TypePrefix = "[]interface{}"
			}
		}

		gt.Fields = append(gt.Fields, sf)
	}

	return
}

func processDeferred() {
	for len(deferredTypes) > 0 {
		startDeferredPaths, _ := stringSetFromMapKeys(deferredTypes)
		for path, deferred := range deferredTypes {
			startDeferredPaths.add(path)
			name := processType(deferred.schema, deferred.name, deferred.desc, path, deferred.parentPath)
			if name != "" {
				delete(deferredTypes, path)
			}
		}

		// if the list is the same as before, we're stuck
		endDeferredPaths, _ := stringSetFromMapKeys(deferredTypes)
		if endDeferredPaths.equals(startDeferredPaths) {
			log.Fatalln("Can't resolve:", startDeferredPaths)
		}
	}
}

func dedupeTypes() {
	for len(typesByName) > 0 {
		// clear all singles first; otherwise some types will not be disambiguated
		for name, dupes := range typesByName {
			if len(dupes) == 1 {
				typesByName.delete(name)
			}
		}

		for name, dupes := range typesByName {
			// delete these dupes; will put back in as necessary in subsequent loop
			typesByName.delete(name)

			for dupePath := range dupes {
				gt := types[dupePath]
				parent := types[gt.parentPath]

				// handle parents before children to avoid stuttering
				if typesByName.has(parent.Name) {
					// add back the child to be processed later
					typesByName.addTo(gt.Name, dupePath)
					continue
				}

				if parent.origTypeName == "" {
					log.Fatalln("Can't disabiguate:", dupes)
				}

				gt.origTypeName = parent.origTypeName + "-" + gt.origTypeName
				gt.Name = generateTypeName(gt.origTypeName)
				types[dupePath] = gt

				// add with new name in case we still have dupes
				typesByName.addTo(gt.Name, dupePath)
			}
		}
	}
}

func parseDefs(s *metaSchema, path string) {
	defs := getTypeSchemas(s.Definitions)
	for defName, defSchema := range defs {
		name := processType(defSchema, defName, defSchema.Description, path+"/definitions/"+defName, path)
		if name == "" {
			deferredTypes[path+"/definitions/"+defName] = deferredType{schema: defSchema, name: defName, desc: defSchema.Description, parentPath: path}
		}
	}
}

func main() {
	kingpin.Parse()

	file, err := ioutil.ReadFile(*inputFile)
	if err != nil {
		log.Fatalln("Error reading file:", err)
	}

	var s metaSchema
	if err := json.Unmarshal(file, &s); err != nil {
		log.Fatalln("Error parsing JSON:", err)
	}

	parseDefs(&s, "#")

	schemaName := strings.Split(filepath.Base(*inputFile), ".")[0]
	if *rootTypeName == "" {
		exported := *packageName != "main"
		*rootTypeName = generateIdentifier(schemaName, exported)
	}
	processType(&s, *rootTypeName, s.Description, "#", "")
	processDeferred()
	dedupeTypes()

	var resultSrc bytes.Buffer
	resultSrc.WriteString(fmt.Sprintln("package", *packageName))
	resultSrc.WriteString(fmt.Sprintf("\n// generated by \"%s\" -- DO NOT EDIT\n", strings.Join(os.Args, " ")))
	resultSrc.WriteString("\n")
	if needTimeImport {
		resultSrc.WriteString("import \"time\"\n")
	}
	typesSlice := make(goTypes, 0, len(types))
	for _, gt := range types {
		typesSlice = append(typesSlice, gt)
	}
	sort.Stable(typesSlice)
	for _, gt := range typesSlice {
		gt.print(&resultSrc)
		resultSrc.WriteString("\n")
	}
	formattedSrc, err := format.Source(resultSrc.Bytes())
	if err != nil {
		fmt.Println(resultSrc.String())
		log.Fatalln("Error running gofmt:", err)
	}

	if *outToStdout {
		fmt.Print(string(formattedSrc))
	} else {
		outputFileName := *outputFile
		if outputFileName == "" {
			compactSchemaName := strings.ToLower(*rootTypeName)
			outputFileName = fmt.Sprintf("%s_schematype.go", compactSchemaName)
		}
		err = ioutil.WriteFile(outputFileName, formattedSrc, 0644)
		if err != nil {
			log.Fatalf("Error writing to %s: %s\n", outputFileName, err)
		}
	}
}
