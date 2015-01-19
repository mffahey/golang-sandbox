package main

import (
    "bytes"
    "fmt"
    "log"
    "net"
    "runtime"
    "time"
)

// Constants
const (
    // local listener ip address (the port for the initial request listener)
    laddr = ":69"
    // default packet read length in bytes. Must be long enough to accommodate the longest possible packet, since a UDP PacketConn may throw away any leftover bytes
    pLen = 2048
)

// FileSystem stuff. Intentionally creating out own file interface instead of using the os.File struct.
// Out File itself implements ReaderWriter to access the underlying data
//TODO: Refactor into its own package later
type File interface {
    Read(b []byte) (int, error)
    Write(b []byte) (int, error)
    Close()
}

type InMemFile struct {
    open chan int
    data bytes.Buffer
}
// InMemFile File implementation
func (f *InMemFile) Read(b []byte) (int, error) {
    return 0, nil
}

func (f *InMemFile) Write(b []byte) (int, error) {
    return 0, nil
}

func (f *InMemFile) Close() {
    
}

type InMemFS struct {
    fileMap map[string]File
}

/* Function which listens for requests, initializes request handlers, and routes incoming packets
*/
func listen(fs *InMemFS, quit chan bool) error {
    fmt.Printf("listen() invoked \n")
    // I'm using explicit declaration here mostly so I don't forget the type of conn
    var conn net.PacketConn
    conn , err := net.ListenPacket("udp", laddr)
    if err != nil {
        log.Fatalf("Unable to open connection on %v ; error was %v\n", laddr, err)
    }
    defer conn.Close()
    packets := make(chan []byte)
    go readPackets(&conn, packets)
    
    fmt.Printf("Listening on %v...", laddr)
    
    
    
    // TODO refactor this
    for {
        select {
            // perform cleanup on quit
            case <-quit:
                fmt.Printf("Closing listener...")
                // TODO: wait for any ongoing requests to complete
                
                return nil
            default:
                time.Sleep(100 * time.Millisecond)
        }
        
        // handle 
    }
}

/* Using the given PacketConn, continuously read incoming packets and push them into the provided channel
*/
func readPackets(conn *net.PacketConn, packets chan []byte) {
    // Read with no timeout
    err := (*conn).SetReadDeadline(time.Time{})
    if err != nil {
        log.Fatalf("Unable to set read timeout on primary connection %v ; error was %v\n", conn, err)
    }
    p := make([]byte, pLen)
    for {
        n, src ,err := (*conn).ReadFrom(p)
        if err != nil {
            log.Fatalf("Encountered error reading packet on primary connection %v ; error was %v\n", conn, err)
        }
        // TODO: remove this line
        fmt.Printf("%v bytes read. Source address was %v", n, src)
        fmt.Printf("string(p[:n])\n")
        packets <- p[:n]
    }
    
}

func assignTID(){}

/* main function performs some initialization tasks and then waits on console input
*/
func main() {
    switch opsys:= runtime.GOOS; opsys {
    case "windows":
        // Do nothing for now
    default:
        fmt.Printf("This application has not yet been tested on OS %s. You have been warned.\n", opsys)
    }
    fmt.Printf("Starting TFTP server...\n")
    // Initialize in-memory file system
    fs := InMemFS { make(map[string]File) }
    // Since its all in memory, no need to defer any cleanup tasks
    
    // Start up request listener routine
    quit := make(chan bool)
    go listen(&fs, quit)
    
    // The main thread will listen for a kill command on standard input
    for {
        time.Sleep(1*time.Second)
        // listen for input
        // TODO: if kill received
        // quit <- true
    }
}