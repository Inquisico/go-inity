# Inity Examples

### Simple

A simple example showing to use Inity to bootstrap a service.

### Hierarchical

A hierarchical example showing how to use Inity to bootstrap a service with multiple dependencies. This is useful when you have a service that has multiple dependencies that need to be started and closed in a specific order.

In this example, HTTP server C requires HTTP server A and B to be running before C, using multiple inity manager we can start server A and B first followed by C. When a signal is sent to close the service, C will be closed first followed by A and B.

### Zerolog

An example showing how to use Inity with Zerolog
