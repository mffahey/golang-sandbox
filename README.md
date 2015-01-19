# golang-sandbox
My own personal sandbox for golang toy-programs and self-education projects

-- Style --
I've noticed that the default go style appears to use 4-space-width tabs as the default format for indentation.  I have used 4 spaces here instead, as is my custom.

-- In memory file-system --
This is a flat file system, effectively a key-value store, backed by a map from string (the file path) to file struct.
The file-system allows lookup of files by key, streamed reading and writing of the files, and locking on files for read or update.
Each file has a data struct (containing the raw bytes), some metadata, and a couple mutexes to manage access. Before reading from a file, we will try to increment the reader count. When writing, we will acquire the mutex. Reader counts and mutexes should always be released as a deferred action.

* TODO, charset?