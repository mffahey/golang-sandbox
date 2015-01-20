# golang-sandbox
My own personal sandbox for golang toy-programs and self-education projects

---- TFTP Server ----
==================
===== SETUP ======
==================

Create a new directory under your $GOPATH, github.com/mffahey/golang-sandbox.  In this directory, check out https://github.com/mffahey/golang-sandbox into a new Git workspace.

To run the server, cd to "$GOPATH/github.com/mffahey/golang-sandbox/tftp/tftps" and execute "go run main.go"
Alternately, run "go install github.com/mffahey/golang-sandbox/tftp/tftps". The server can then be run via the generated tftps executable (tftps on Unix Systems, tftps.exe on Win)

==================
===== USAGE ======
==================
After installing by running "go install" from the github.com/mffahey/golang-sandbox/tftp/tftps, t
Alternatively, 

The following command line args are supported:
[d | -d] turn on debug level logging
[--disableOverwrite]  Do not allow new write requests on existing files. Write requests to existing files will result in an FNF error
[--disableLockOnRead]  Allow reads to happen concurrently with writes. This could result in an inconsistent view of the underlying file data across multiple concurrent reads.

==================
===== NOTES ======
==================
-- Style --
I've noticed that the default go style appears to use 4-space-width tabs as the default format for indentation.  I have used 4 spaces here instead, as is my custom. Some of my editors auto-replace tabs with spaces on save/format. I'll try to remember to run gofmt before committing.

-- In memory file-system --
This is a flat file system which simply aliases a map from string (the file path) to a file struct.
The file struct consists of a slice of bytes that contains the backing file data and a RWMutex to manage concurrent read/write access.
Files are initialized with Data == nil. A file with an uninitialized Data field is considered "unfinished". This allow us to treat unfinished files as invisible to read requests, wile still accessing their mutex to prevent concurrent writes.
If a write transaction exits with an error, an unfinished file may be left in the file system. In a future extension, we could establish a routine to clean up these unifinished files. Otherwise, we could clean up these files immediately. That would require that any write transactions against an existing file would have to verify that a file is finished AFTER acquiring a lock on it, and, if it is unfinished, we would have to discard the file and re-enter the write transaction logic form the beginning.

==================
===== TODOS ======
==================
* TODO: suppress packet listener errors on main listener about reading from a closed connection when the server shuts down normally
* TODO: sanitize and standardize errors returned to client
* TODO: establish filesystem cleanup routine for unfinished files.
* TODO: listen a little longer at the end of a write transaction to make sure the client doesn't need us to re-ACK the last DATA packet
* TODO: refactor retrying-loop for the ACK and DATA packet writes into their own functions
