Contains integration tests to exercise and test capabilities in this repo and to act as examples of how the integrations
tests for a given product can be built using the capabilities integration testing framework.

### Usage
1. Run a postgres instance as described in [Chainlink repo](https://github.com/smartcontractkit/chainlink) and then setup the
   database by running `go run ./utils/databasesetup/main.go local db preparetest` from the integration_tests directory.
 
2. Set the database path in your environment or the environment of the test runner
```
export CL_DATABASE_URL=postgresql://chainlink_dev:insecurepassword@localhost:5432/chainlink_development_test?sslmode=disable
```
3. Run specific tests from root as shown below or directly from an IDE as you would any other unit test
```
./nx run integration_tests:test -run ^Test_CronTrigger$ -v
```