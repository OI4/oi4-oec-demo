# Open Industry 4.0 Alliance - Open Edge Computing Demos

## Installation

### GitHub package repository access
Dependencies to the oi4-service are available in the GitHub package repository. As the OI4 repositories are private, you need to authenticate to the GitHub repository using a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token) (PAT).
The PAT must have the 'read:packages'.
![](docs/pat_access_rights.png)

The install and build scripts will use the preconfigured .npmrc file to access the GitHub package repository. The PAT must be stored in the .env file in the project root with the key `PACKAGES_AUTH_TOKEN`. As the .env file is not always applied when using the console, the PAT should also be set as an environment variable.
The bash script `setenv.sh` will process the .env file and set the PAT as an environment variable. On Linux based systems just run `source ./scripts/setenv.sh`.


