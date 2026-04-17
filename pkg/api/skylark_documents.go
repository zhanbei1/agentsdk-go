package api

import (
	"fmt"
	"strings"

	"github.com/stellarlinkco/agentsdk-go/pkg/runtime/skills"
	"github.com/stellarlinkco/agentsdk-go/pkg/skylark"
	"github.com/stellarlinkco/agentsdk-go/pkg/tool"
)

func buildSkylarkDocuments(memory string, rules string, skReg *skills.Registry, tools []tool.Tool, extra ...[]skylark.Document) []skylark.Document {
	var docs []skylark.Document
	if strings.TrimSpace(memory) != "" {
		docs = append(docs, skylark.Document{
			ID:    "memory:agents-md",
			Kind:  skylark.KindMemory,
			Title: "AGENTS.md memory",
			Text:  strings.TrimSpace(memory),
		})
	}
	if r := strings.TrimSpace(rules); r != "" {
		docs = append(docs, skylark.Document{
			ID:    "context:project-rules",
			Kind:  skylark.KindContext,
			Title: "Project rules",
			Text:  r,
		})
		docs = append(docs, skylark.Document{
			ID:    "document:project-rules",
			Kind:  skylark.KindDocument,
			Title: "Project rules (document)",
			Text:  r,
		})
	}
	if skReg != nil {
		for _, def := range skReg.List() {
			id := fmt.Sprintf("skill:%s", def.Name)
			var b strings.Builder
			b.WriteString(fmt.Sprintf("Name: %s\n", def.Name))
			if strings.TrimSpace(def.Description) != "" {
				b.WriteString(def.Description)
				b.WriteByte('\n')
			}
			if len(def.Metadata) > 0 {
				b.WriteString("Metadata:\n")
				for k, v := range def.Metadata {
					fmt.Fprintf(&b, "  %s: %s\n", k, v)
				}
			}
			if b.Len() == 0 {
				continue
			}
			docs = append(docs, skylark.Document{
				ID:    id,
				Kind:  skylark.KindSkill,
				Title: def.Name,
				Text:  b.String(),
				Meta: map[string]string{
					"skill": def.Name,
				},
			})
		}
	}
	skip := map[string]struct{}{
		"retrieve_knowledge":    {},
		"retrieve_capabilities": {},
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.Name())
		if name == "" {
			continue
		}
		if _, ok := skip[strings.ToLower(name)]; ok {
			continue
		}
		schema := ""
		if t.Schema() != nil {
			schema = fmt.Sprintf("%v", t.Schema())
		}
		text := strings.TrimSpace(t.Description()) + "\n" + schema
		kind := skylark.KindTool
		meta := map[string]string{"tool": name}
		if strings.Contains(name, "__") {
			kind = skylark.KindMCP
			meta["mcp"] = "true"
		}
		docs = append(docs, skylark.Document{
			ID:    fmt.Sprintf("%s:%s", kind, name),
			Kind:  kind,
			Title: name,
			Text:  text,
			Meta:  meta,
		})
	}
	for _, set := range extra {
		if len(set) == 0 {
			continue
		}
		docs = append(docs, set...)
	}
	return docs
}
