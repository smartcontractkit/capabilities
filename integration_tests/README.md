Contains integration tests to exercise and test capabilities in this repo.

### Usage
1. Run a postgres instance as described in [Chainlink repo](https://github.com/smartcontractkit/chainlink) and then setup  the
   database by running `go run github.com/smartcontractkit/chainlink/v2 local db preparetest` from the integration_tests directory.
 
2. Set the database path in your environment or the environment of the test runner
```
export CL_DATABASE_URL=postgresql://chainlink_dev:insecurepassword@localhost:5432/chainlink_development_test?sslmode=disable
```
3. Run specific tests from root as shown below or directly from an IDE as you would any other unit test
```
./nx run integration_tests:test -run ^Test_CronTrigger$ -v
```

If a test killed part way through it's possible that it zombie standard capabilities are still running. To kill these run the following command:
```
./scripts/killZombieProcesses.sh
```

### Test setup
![Diagram to illustrate the communication mechanisms and processes involved in running the integration tests](./integrationtestsdiagram.png)