// LOCAL_COLLECTOR_HOST_ID is the fixed id of the machine where the node
// serving this UI collects its own metrics (backend hosts.LocalCollectorHostID).
// Remote cluster peers are id>=2. Used to decide local-vs-remote behaviour:
// the local host streams live over SSE, remote peers are polled / handled via
// the replicated rows.
export const LOCAL_COLLECTOR_HOST_ID = 1;
