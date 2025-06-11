Contains integration tests designed to explore the behaviour of a multi-node Don(s) under load.  The tests should be run as per other integration
tests (as detailed in the integration_tests README.md) but with the addition of the following steps:

1. Run the generateWorkflowTestsFiles.sh before running the tests in load_test.go, ensure that enough workflow files are
   generated for the test, eg if the test requires that 100 unique workflows are loaded then run the following command:

```bash
./workflows/generateWorkflowTestsFiles.sh 100
```

Before checking in any changes run the removeGeneratedFiles.sh to remove the generated files as the generated files
in sum are large and should not be checked in.

2. Set the LOAD_TEST_RESULTS_DIR environment variable to the directory where the test results should be written to.

```bash
export LOAD_TEST_RESULTS_DIR=/path/to/results
```

3. Comment out the t.Skip() line at the beginning of the test that should be run. Make sure to uncomment the line
    before checking in the code to prevent CI from running the load tests.