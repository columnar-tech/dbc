# Analytics

dbc collects basic, anonymized information in order to help us better understand how dbc is being used.

## What data do we collect?

When you use dbc to search for or install a driver, dbc communicates with Columnar's CDN which is hosted using [Amazon Web Services](http://aws.amazon.com). Requests to the CDN include information such as:

- IP Address
- dbc version
- Operating system name and architecture (e.g., linux/amd64)
- A non-reversible but stable per-machine ID and a random UUID (generated once per install)

## Where does it get stored?

The above data is stored as part of the normal recording of HTTP request logs to the CDN. Logs are stored in a private S3 bucket which is only accessible to authorized Columnar staff.

## How long do we collect it for?

Logs are currently not deleted but we plan to set up automatic log pruning and will update this section when we do that.

## How do I opt out?

There's currently no mechanism to opt out of this data collection. If you have any questions or concerns, please reach out to us at [hello@columnar.tech](mailto:hello@columnar.tech).
