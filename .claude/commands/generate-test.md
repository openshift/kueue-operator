# Generate Manual Testing Documentation

Generate an HTML manual testing document from a Ginkgo E2E test file.

## Process

1. **Ask the user to choose the test location:**
   - **Downstream:** Tests in `kueue-operator/test/e2e/` (operator-specific tests)
   - **Upstream:** Tests in `kueue-operator/upstream/kueue/src/test/e2e/` (upstream kueue tests)

2. **List available test files:**
   - Search the selected directory for files ending with `_test.go`
   - Display them in a numbered list
   - Ask the user to select one by number or name

3. **Generate comprehensive HTML documentation:**
   - Read and analyze the selected test file
   - Extract test scenarios from Describe/Context/It blocks
   - Create an HTML file with manual testing instructions
   - **Target OpenShift Container Platform (OCP) 4.21** (latest released version)

## HTML Documentation Requirements

The generated HTML should follow the format from the reference document and include:

### Structure
- **Title:** Manual Test: [Test Name] - Simple, direct title
- **Test Overview section** with metadata box containing:
  - Test ID (e.g., E2E-FEATURE-001)
  - Feature being tested
  - Test Type (Manual UI Test)
  - Platform: OpenShift Container Platform (OCP) 4.21
  - Duration estimate
- **Purpose:** Brief description of what the test covers
- **Test Strategy:** Numbered list of high-level approach
- **Prerequisites:** Required setup with checkmark bullets
- **Test Setup:** Phased approach with clear steps
- **Test Execution:** Step-by-step testing procedures
- **Verification Steps:** Checkpoints with expected results
- **Success Criteria:** Table format with Pass/Fail checkboxes
- **Test Cleanup:** Resource deletion steps
- **References:** Source info and documentation links

### Step-by-Step Format
Each test step should include:
1. **Clear step numbering** (e.g., Step 1.1, Step 1.2)
2. **Command blocks** with copy functionality
3. **Expected Result boxes** for verification
4. **Tables** for structured data presentation
5. **Colored callout boxes:**
   - `.metadata` - Blue info boxes
   - `.checkpoint` - Green checkpoint boxes
   - `.warning` - Yellow warning boxes
   - `.important` - Red important notes
   - `.expected-result` - Green expected result boxes

### Testing Approaches
Focus primarily on **oc CLI commands** with:
- Complete `oc` commands with all flags
- Commands to verify results
- Expected output examples
- Inline YAML manifests using heredoc format
- UI guidance where applicable (but CLI-focused)

## Style Guidelines

Use the specific CSS styling from the reference document:
- Calibri/Arial font family
- Blue color scheme (#0066cc primary)
- Clean table styling with zebra striping
- Proper code block formatting with left border
- Print-friendly design
- Section dividers for clear organization
- Professional documentation appearance

## Important Context

- Follow kueue-operator patterns (check existing test YAML files in `test/` directory)
- Use proper security contexts (RunAsNonRoot, SeccompProfile, etc.)
- Include proper labels for Kueue resources
- Reference actual resource quotas and limits from the test
- Consider namespace labeling requirements (`kueue.openshift.io/managed: "true"`)
- Include LocalQueue and ClusterQueue setup if needed

## File Naming

Save the HTML file as:
- `MANUAL_TEST_[TESTNAME].html` in the same directory as the test file
- Use uppercase and hyphens (e.g., `MANUAL_TEST_LOCAL-QUEUE-DEFAULTING.html`)

## Example Structure

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <title>Manual Test: [Test Name]</title>
    <style>
        /* Use exact CSS from reference document */
        body {
            font-family: 'Calibri', 'Arial', sans-serif;
            line-height: 1.6;
            max-width: 900px;
            margin: 40px auto;
            padding: 20px;
            color: #333;
        }
        /* ... rest of CSS from reference ... */
    </style>
</head>
<body>
    <h1>Manual Test: [Test Name]</h1>

    <h2>Test Overview</h2>
    <div class="metadata">
        <p><strong>Test ID:</strong> E2E-[FEATURE]-001</p>
        <p><strong>Feature:</strong> [Feature Name]</p>
        <p><strong>Test Type:</strong> Manual CLI Test</p>
        <p><strong>Platform:</strong> OpenShift Container Platform (OCP) 4.21</p>
        <p><strong>Duration:</strong> ~X-Y minutes</p>
    </div>

    <h3>Purpose</h3>
    <!-- Brief test description -->

    <h3>Test Strategy</h3>
    <ol>
        <li>Setup phase</li>
        <li>Execution phase</li>
        <li>Verification phase</li>
    </ol>

    <div class="section-divider"></div>

    <h2>Prerequisites</h2>
    <ul>
        <li>✅ Access to OpenShift Container Platform (OCP) 4.21</li>
        <li>✅ Authenticated <code>oc</code> CLI session</li>
    </ul>

    <div class="section-divider"></div>

    <h2>Test Setup</h2>
    <h3>Phase 1: [Phase Name]</h3>
    <h4>Step 1.1: [Step Name]</h4>
    <pre><code>oc apply -f - &lt;&lt;EOF
[YAML content]
EOF</code></pre>

    <div class="expected-result">
        <strong>Expected Result:</strong> [Expected outcome]
    </div>

    <h2>Test Execution</h2>
    <!-- Test steps -->

    <h2>Verification Steps</h2>
    <div class="checkpoint">
        <h4>✅ Checkpoint 1: [Check Name]</h4>
        <!-- Verification details -->
    </div>

    <h2>Success Criteria</h2>
    <table>
        <tr><th>Requirement</th><th>Status</th><th>Notes</th></tr>
        <tr><td>[Requirement]</td><td>☐ Pass ☐ Fail</td><td>[Notes]</td></tr>
    </table>

    <h2>Test Cleanup</h2>
    <!-- Cleanup steps -->

    <h2>Troubleshooting</h2>
    <h3>Common Issues and Solutions</h3>
    <div class="scenario">
        <div class="scenario-header">Issue: [Issue Name]</div>
        <div class="scenario-content">
            <p><strong>Symptoms:</strong> [Description of symptoms]</p>
            <p><strong>Possible Causes:</strong></p>
            <ul>
                <li>[Cause 1]</li>
                <li>[Cause 2]</li>
            </ul>
            <p><strong>Investigation Steps:</strong></p>
            <pre><code>[Commands to investigate]</code></pre>
            <p><strong>Solution:</strong> [How to resolve]</p>
        </div>
    </div>

    <h2>References</h2>
    <!-- Source info and links -->
</body>
</html>
```

## After Generation

- Display the path to the created HTML file
- Offer to open it in a browser
- Ask if the user wants to generate documentation for another test

