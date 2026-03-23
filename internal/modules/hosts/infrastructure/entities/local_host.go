package entities

// LocalCollectorHostID is the fixed primary key for the machine where this server collects metrics.
// Remote cluster agents use other rows (Join / UpsertHost). Do not reassign this ID.
const LocalCollectorHostID uint = 1
