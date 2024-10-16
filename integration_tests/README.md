Contains integration tests to exercise and test capabilities in this repo and to act as examples of how the integrations
tests for a given product can be built using the capabilities integration testing framework.

### Usage
1. Initialize and ensure a clean local testdb from the [Chainlink repo](https://github.com/smartcontractkit/chainlink) using `make setup-testdb`
2. Set the database path in your environment or the environment of the test runner
```
export CL_DATABASE_URL=postgresql://chainlink_dev:insecurepassword@localhost:5432/chainlink_development_test?sslmode=disable
```
3. Run specific tests from root as shown below or directly from an IDE as you would any other unit test
```
./nx run integration_tests:test -run ^Test_CronTrigger$ -v
```