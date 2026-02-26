---
description: Generate a comprehensive code coverage summary for all test runs
---

Run the unit tests with coverage and generate a comprehensive summary report.

Execute the following steps:

1. Run unit tests with coverage:
   ```bash
   go test -mod=vendor -coverprofile=coverage.out -covermode=atomic ./pkg/... 2>&1 | tee /tmp/test_output.txt
   ```

2. Get the overall coverage percentage:
   ```bash
   go tool cover -func=coverage.out | tail -1
   ```

3. Generate and display a comprehensive coverage summary that includes:

   **Overall Coverage**
   - Total statement coverage percentage

   **Unit Test Coverage**
   - List packages with tests and their coverage percentages
   - Categorize by coverage level (High ≥90%, Medium 80-89%, Low <80%)
   - List packages with 0% coverage
   - Distinguish between core packages and generated code

   **E2E Tests**
   - Count and list E2E test files
   - Show file sizes for major test files

   **Detailed File Coverage**
   - Show function-level coverage for tested packages

   **Test Commands**
   - Document available test commands (unit, e2e, disruptive, etc.)

   **Key Findings**
   - Strengths: What's well-tested
   - Critical Gaps: What needs tests
   - Recommendations: Priority improvements

   **Summary Statistics**
   - Total packages scanned
   - Percentage with tests
   - Percentage with 0% coverage
   - Generated code packages vs core logic packages

Format the output as a clear, well-structured markdown report with tables, bullet points, and sections.
