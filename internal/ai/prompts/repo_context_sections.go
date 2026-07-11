package prompts

// RepoContextStructure defines the fixed sections of the repo context document.
// Used verbatim in both the generator and the patcher system prompts.
//
// teamSynced is true when the repo is shared across a team, meaning the input
// sessions come from different developers rather than one. In that case the
// document describes the team's shared, common usage instead of one person's
// individual style - only patterns evidenced across multiple developers count.
func RepoContextStructure(teamSynced bool) string {
	who := "this developer"
	writesHeading := "How This Developer Writes Requests"
	writesIntro := "Stable patterns in how this developer phrases prompts that affect how an agent should interpret future tasks."
	scopeNote := ""
	if teamSynced {
		who = "this team"
		writesHeading = "How This Team Writes Requests"
		writesIntro = "Stable patterns shared across this team's developers in how they phrase prompts that affect how an agent should interpret future tasks."
		scopeNote = "\nSessions in the input come from different developers on the same team. Only include a term or pattern if it recurs across multiple developers - do not attribute one person's individual habit or wording to the whole team."
	}

	return `The document uses these fixed sections:

# Agent Context
One sentence describing what this repository does.
Do not include implementation details, file paths, setup steps, or task history.

## Terminology
Words and shorthand ` + who + ` uses with non-obvious or project-specific meaning.
Format: * term → what they mean when they say it in a prompt
Only include terms where the meaning would not be obvious to an agent seeing the repository cold.
Do not include ordinary technology names, file names, package names, or generic product terms unless ` + who + ` uses them with a special meaning.` + scopeNote + `

## ` + writesHeading + `
` + writesIntro + `
Include:
* How much context they provide vs. expect the agent to infer.
* How they signal scope: what is in, what is out.
* What short or terse requests usually imply.
* How they express dissatisfaction and what kind of correction they expect.
Do not include one-off preferences, task-specific decisions, or implementation guidance.` + scopeNote + `

## Common Misinterpretations
Recurring mistakes agents make when working with this repository or ` + who + `.
Include:
* Requests that look like X but usually mean Y.
* Scope mistakes: over-building, under-building, wrong layer, wrong subsystem.
* Assumptions agents make that are usually incorrect here.
Do not include mistakes from a single task unless feedback shows they are likely to recur.

## Hard Limits
Strict constraints ` + who + ` consistently rejects or corrects.
Include only durable "never do this" / "always respect this" constraints.
Do not include soft preferences, normal coding style, or advice that belongs in a topic context.

Repo context must not contain:
- start-here files
- edit targets
- test files
- workflows
- bug-specific lessons
- benchmark-specific instructions
- recent session summaries`
}
