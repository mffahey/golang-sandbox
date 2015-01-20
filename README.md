# golang-sandbox
My own personal sandbox for golang toy-programs and self-education projects


---- TFTP Server ----
==================
===== SETUP ======
==================
TODO

==================
===== NOTES ======
==================
-- Style --
I've noticed that the default go style appears to use 4-space-width tabs as the default format for indentation.  I have used 4 spaces here instead, as is my custom. Some of my editors auto-replace tabs with spaces on save/format. I'll try to remember to run gofmt before committing.

-- In memory file-system --
This is a flat file system, effectively a key-value store, backed by a map from string (the file path) to file struct.
The file-system allows lookup of files by key, streamed reading and writing of the files, and locking on files for read or update.

==================
===== TODOS ======
==================
* TODO: refactor into packages
* TODO: suppress packet listener errors about reading from a closed connection when the server shuts down normally
* TODO: sanitize and standardize errors returned to client
* TODO: add support for setting overwriteEnabled via command line
* TODO: add support for setting lockFileForRead via command line
* TODO: establish filesystem cleanup routine for unfinished files.
* TODO: listen a little longer at the end of a write transaction to make sure the client doesn't need us to re-ACK the last DATA packet
* TODO: refactor the retrying ACK and DATA packet writes into their own functions
* TODO: write a cleaner 
