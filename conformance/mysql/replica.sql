-- Makes this server a replica of the MySQL service: it replays the primary's
-- binary log from the start, by GTID. The primary's image initializes it
-- without logging, so the log holds only what the tests write.
CHANGE REPLICATION SOURCE TO
  SOURCE_HOST = 'mysql', SOURCE_USER = 'root', SOURCE_PASSWORD = 'root',
  SOURCE_AUTO_POSITION = 1, GET_SOURCE_PUBLIC_KEY = 1, SOURCE_CONNECT_RETRY = 1;
START REPLICA;
