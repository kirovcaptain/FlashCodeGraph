## Project Path Resolution

1. Call `list_projects` to get all indexed projects
2. Match the user's project reference to an entry (name, abbreviation, or fuzzy match)
3. If ambiguous → ask user to clarify
4. If no match → ask user for the path
5. If user does not specify a project → ask which project
