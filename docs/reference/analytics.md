# Analytics

Columnar collects basic, anonymized information in order to help us better understand how dbc is being used.

## What data do we collect?

When you use dbc to search for or install a driver, dbc sends requests to the [driver registries](../concepts/driver_registry.md) it's configured to use. By default, these are:

- The public ADBC driver registry (https://dbc-cdn.columnar.tech), which is affiliated with the [ADBC Driver Foundry](https://adbc-drivers.org) and hosted by Columnar
- Columnar's private driver registry (https://dbc-cdn-private.columnar.tech)

Both registries are served from a CDN that Columnar operates on [Amazon Web Services](http://aws.amazon.com). Requests to the CDN include information such as:

- IP address
- dbc version
- Operating system name and architecture (e.g., linux/amd64)
- A non-reversible but stable per-machine ID and a random UUID (generated once per install)

Tools that access the registries without dbc are asked to set a user agent that identifies them. See the [FAQ](../getting_started/faq.md#can-i-download-drivers-directly-from-the-driver-registries).

## Where does it get stored?

The above data is stored as part of the normal recording of HTTP request logs to the CDN. Logs are stored in a private S3 bucket which is only accessible to authorized Columnar staff.

## How long do we collect it for?

Logs are currently not deleted but we plan to set up automatic log pruning and will update this section when we do that.

## How do I opt out?

There's currently no mechanism to opt out of this data collection. If you have any questions or concerns, please reach out to us at [privacy@columnar.tech](mailto:privacy@columnar.tech).
