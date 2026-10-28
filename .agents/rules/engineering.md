# Engineering Standards & Architecture Rules

## Core Principles
1. **KISS (Keep It Simple, Stupid):**
   - Favor straightforward, readable code over clever or overly complex abstractions.
   - Use standard library constructs wherever possible.

2. **YAGNI (You Aren't Gonna Need It):**
   - Do not write speculative code or features that are not currently needed.
   - Avoid premature generalizations and unnecessary interfaces.

3. **SOLID:**
   - **Single Responsibility (SRP):** Each file, package, and struct must have one distinct, well-defined responsibility.
   - **Open/Closed:** Prefer composable functions and structs over sprawling monolithic types.
   - **Liskov Substitution & Interface Segregation:** Keep interfaces small (1-3 methods, e.g., `io.Reader`, `io.Writer`).
   - **Dependency Inversion:** Depend on abstractions or pass dependencies explicitly.

4. **DRY (Don't Repeat Yourself):**
   - Consolidate duplicated logic without sacrificing readability or creating coupled abstractions.

## File Size Constraint
- **Maximum 250 Lines:** No source or test file may exceed **250 lines of code**.
- When a file approaches 200 lines, decompose it logically by responsibility into cohesive peer files within the same package.

## Code & Documentation Quality
- **Language:** Code comments and documentation must always be in clear, concise English.
- **Comments:** Short, purposeful comments explaining *why*, not just repeating *what* the code does.
- **Go Standards:** Strict adherence to `go fmt`, `go vet`, and standard Go naming conventions.
- **Zero Regressions:** Never sacrifice correctness or edge-case handling when refactoring. Run tests before and after edits.
