package mcp

import (
	"fmt"
	"strings"
)

// promptDefs is the static catalogue of MCP prompts we advertise. Each
// prompt renders a single user-role message with arguments interpolated —
// the LLM reads it and decides which tools to call. The aim is to keep the
// rendered text terse: a sentence of intent, then a list of which tools to
// reach for first. The model fills in the rest.
type promptDef struct {
	name        string
	description string
	arguments   []PromptArgument
	// render returns the user-facing message text. Caller has already
	// validated that all required arguments are present.
	render func(args map[string]string) string
}

var promptDefs = []promptDef{
	{
		name:        "devrecall-recall",
		description: "Search DevRecall for activity matching a query and report the most relevant hits with citations.",
		arguments: []PromptArgument{
			{Name: "query", Description: "What to recall (concept, ticket key, person, decision, etc.)", Required: true},
		},
		render: func(args map[string]string) string {
			q := strings.TrimSpace(args["query"])
			return fmt.Sprintf(
				"Search my DevRecall index for: %q.\n\n"+
					"1. Call current_time first so relative dates make sense.\n"+
					"2. Try semantic_search_activities for fuzzy matches; fall back to search_activities for keyword-shaped queries.\n"+
					"3. For the top 3–5 hits, call get_activity to read the body.\n"+
					"4. For any activity that carries a ticket key, call get_related_activities to surface the full chain.\n"+
					"5. Summarize the findings with source, date, and a short quote where useful — don't bury the answer.",
				q,
			)
		},
	},
	{
		name:        "devrecall-context",
		description: "Inject a brief of recent activity so the assistant starts knowing what's been going on.",
		arguments: []PromptArgument{
			{Name: "days", Description: "How many days back to summarise (default 7)", Required: false},
		},
		render: func(args map[string]string) string {
			days := strings.TrimSpace(args["days"])
			if days == "" {
				days = "7"
			}
			return fmt.Sprintf(
				"Brief me on what I've been working on in the last %s days.\n\n"+
					"1. Call current_time, then list_activities with start = (now - %s days), limit 50.\n"+
					"2. Group by source and summarise in 4–6 bullets. Highlight anything that looks like a decision (call recent_decisions if needed).\n"+
					"3. Don't list every commit — focus on themes and shipped work.\n"+
					"4. End with one line of open threads (recent activity without a follow-up).",
				days, days,
			)
		},
	},
	{
		name:        "devrecall-log",
		description: "Capture a note into DevRecall without leaving the editor.",
		arguments: []PromptArgument{
			{Name: "text", Description: "The note body — decision, observation, conversation summary", Required: true},
		},
		render: func(args map[string]string) string {
			text := strings.TrimSpace(args["text"])
			// Quote-escape so the LLM passes the text through faithfully when
			// it composes the tool call.
			escaped := strings.ReplaceAll(text, `"`, `\"`)
			return fmt.Sprintf(
				"Log this into DevRecall as a manual note:\n\n%q\n\n"+
					"Call log_event with text=\"%s\". After it returns, confirm the activity_id and one-line title.",
				text, escaped,
			)
		},
	},
}

// listPrompts builds the response for prompts/list.
func listPrompts() ListPromptsResult {
	descriptors := make([]PromptDescriptor, 0, len(promptDefs))
	for _, p := range promptDefs {
		descriptors = append(descriptors, PromptDescriptor{
			Name:        p.name,
			Description: p.description,
			Arguments:   p.arguments,
		})
	}
	return ListPromptsResult{Prompts: descriptors}
}

// getPrompt renders the named prompt with the supplied arguments.
func getPrompt(name string, args map[string]string) (GetPromptResult, *RPCError) {
	for _, p := range promptDefs {
		if p.name != name {
			continue
		}
		for _, arg := range p.arguments {
			if arg.Required {
				v, ok := args[arg.Name]
				if !ok || strings.TrimSpace(v) == "" {
					return GetPromptResult{}, &RPCError{
						Code:    ErrInvalidParams,
						Message: fmt.Sprintf("prompts/get %s: missing required argument %q", name, arg.Name),
					}
				}
			}
		}
		return GetPromptResult{
			Description: p.description,
			Messages: []PromptMessage{{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: p.render(args)},
			}},
		}, nil
	}
	return GetPromptResult{}, &RPCError{
		Code:    ErrInvalidParams,
		Message: "prompts/get: unknown prompt " + name,
	}
}
