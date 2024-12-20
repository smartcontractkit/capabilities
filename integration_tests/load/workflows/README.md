# wasm-load-tests
Run the generateWorkflowTestsFiles.sh before running the tests in load_test.go, ensure that enough workflow files are
generated for the test, eg if the test requires that 100 unique workflows are loaded then run the following command:

```bash
./generateWorkflowTestsFiles.sh 100
```

Before checking in any changes run the removeGeneratedFiles.sh to remove the generated files as the generated files 
in sum are large and should not be checked in.

