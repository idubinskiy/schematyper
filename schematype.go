package main

type Schema struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Type        interface{}        `json:"type"`
	Required    []string           `json:"required"`
	Properties  map[string]*Schema `json:"properties"`
	Items       interface{}        `json:"items"`
	Format      string             `json:"format"`
}
