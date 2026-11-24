Verbatim copies of ranxi v2.21.6 migrations 333, 334, 336 and 342. Kitty ships the final schema in
migration 365 only; these files exist so the upgrade-path tests keep exercising the Go code against
the schema shape and upgrade data a ranxi installation would have. They are never applied to a real database.
