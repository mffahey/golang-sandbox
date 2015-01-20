package main

import (
    "bytes"
    "fmt"
    "log"
    "net"
    "os"
    "runtime"
    "strings"
    "sync"
    "time"
    
    "github.com/mffahey/golang-sandbox/tftp/tftpconst"
    "github.com/mffahey/golang-sandbox/tftp/debug"
    "github.com/mffahey/golang-sandbox/tftp/packets"
)


const (
    // default packet read length in bytes. Must be long enough to accommodate the longest possible packet, since a UDP PacketConn may throw away any leftover bytes
    pLen = 2048
    blockSize = 512
    
    // default ack timeout for sent packets (10 seconds)
    timeoutDuration = 10000 * time.Millisecond
    // default retry count for un-acked packets 
    retry = 2
)

func timeout() time.Time {
    return time.Now().Add(timeoutDuration);
}

/*
 * Global vars for runtime configuration
 */
 
// Can we overwrite pre-existing files?
var overwriteEnabled = true;

// Lock on read?
// If true, a write request will block until all read requests on a resource have fully completed, and vice versa, ensuring that all read requests concurrent read requests see the same view of the data.
// If false, reads will be non blocking. Read requests will still return a self-consistent view of the file, but the data returned may be out of date by the time the client receives it.
var lockFileForRead = true;


// FileSystem stuff. Note that our FS is just a map, and out simplified in memory file does not implement os.File
type InMemFS map[string]*InMemFile

type InMemFile struct {
    lock *sync.RWMutex
    data []byte
}

func (f *InMemFile) isFinished() bool {
    return f.data != nil
}

/* 
 * Function which listens for requests and calls into request handlers 
 */
func listen(fs *InMemFS, quit chan bool) error {
    fmt.Printf("listen() invoked \n")
    // Open a net.PacketConn. This will be the default request listener
    conn , err := net.ListenPacket("udp", tftpconst.Laddr)
    if err != nil {
        log.Fatalf("Unable to open connection on %v ; error was %v\n", tftpconst.Laddr, err)
    }
    defer conn.Close()
    packets := make(chan *packets.SourcedPacket)
    go listenPackets(&conn, packets)
    
    fmt.Printf("Listening on %v...", tftpconst.Laddr)
    // WaitGroup for tracking whether there are any outstanding requests
    var transWG sync.WaitGroup
    for {
        select {
            case p := <- packets :
                debug.Debugln("listen: Packet popped was", string(p.Data), "from", p.Src);
                transWG.Add(1)
                go func() {
                    defer transWG.Done()
                    handleTransaction(p, fs)
                } ()
            case <-quit :
                fmt.Printf("Waiting for outstanding transactions to complete...")
                transWG.Wait()
                return nil
        }
        
        // handle 
    }
}

// Request handler functions

/* 
 * Using the given PacketConn, continuously read incoming packets and push them into the provided channel
 */
func listenPackets(conn *net.PacketConn, packetsChan chan *packets.SourcedPacket) {
    // Read with no timeout
    err := (*conn).SetReadDeadline(time.Time{})
    if err != nil {
        log.Fatalf("Unable to set read timeout on primary connection %v ; error was %v\n", conn, err)
    }
    pContent := make([]byte, pLen)
    for {
        n, src ,err := (*conn).ReadFrom(pContent)
        if err != nil {
            log.Fatalf("Encountered error reading packet on primary connection %v ; error was %v\n", conn, err)
        }
        debug.Debugln(n, " bytes read. Source address was ", src)
        debug.Debugln("Content:", string(pContent[:n]))
        packetsChan <- &packets.SourcedPacket{pContent[:n], src}
    }
}

/*
 * Handles the scope of a single transaction, whether read, write, or other
 */
func handleTransaction(p *packets.SourcedPacket, fs *InMemFS) {
    // Get new net.PacketConn for this transaction
    // ":0" to delegate un-socketed port assignment to the OS -- Attr. a hot tip from Justen Meden
    tConn , err := net.ListenPacket("udp", ":0")
    if err != nil {
        fmt.Printf("Unable to open network connection ; error was %v\n", err)
        // This request will be discarded. Caller will need to retry.
        // Open Item: Should we fatal instead of continuing?
        return
    }
    defer tConn.Close()
    err = tConn.SetDeadline(timeout())
    if err != nil {
        packets.WriteErrorPacket(&tConn, p, tftpconst.ErrIllegalOp, err.Error(), timeout())
        return
    }
    op, err := p.Opcode()
    if err != nil {
        packets.WriteErrorPacket(&tConn, p, tftpconst.ErrIllegalOp, err.Error(), timeout())
        return
    }
    switch op {
        case tftpconst.OpRrq:
            debug.Debugln("handleTransaction: Handling RRQ request.");
            handleRead(&tConn, p, fs)
        case tftpconst.OpWrq: 
            debug.Debugln("handleTransaction: Handling WRQ request.");
            handleWrite(&tConn, p, fs)
        default: 
            errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
            debug.Debugln("handleTransaction:", errMsg, "Sending ERROR response.")
            packets.WriteErrorPacket(&tConn, p, tftpconst.ErrIllegalOp, errMsg, timeout())
    }
}

/*
 * Handle control flow for read transaction type
 */
func handleRead(conn *net.PacketConn, p *packets.SourcedPacket, fs *InMemFS) {
    filename, tmode, err := p.ReqParams()
    if err != nil {
        packets.WriteErrorPacket(conn, p, tftpconst.ErrIllegalOp, err.Error(), timeout())
        return
    }
    if "octet" != strings.ToLower(tmode) {
        errMsg := fmt.Sprintf("Unsupported TFTP mode %v. Only octet mode is supported by this server.", tmode)
        packets.WriteErrorPacket(conn, p, tftpconst.ErrNDef, errMsg, timeout())
        return
    }
    file, exists := (*fs)[filename] 
    if !exists || !file.isFinished() {
        errMsg := fmt.Sprintf("No file exists for filename: %v", filename)
        packets.WriteErrorPacket(conn, p, tftpconst.ErrFNF, errMsg, timeout())
        return
    }
    if lockFileForRead {
        file.lock.RLock()
        defer file.lock.RUnlock()
    }
    fileDataBuf := &bytes.Buffer{}
    fileDataBuf.Write(file.data)
    
    pData := make([]byte, blockSize)
    block := uint16(1)
    prevPacketRec := p
    retryCount := 0
    for pDataLen := blockSize; pDataLen == blockSize ; {
        n, err := fileDataBuf.Read(pData)
        if err != nil {
            packets.WriteErrorPacket(conn, prevPacketRec, tftpconst.ErrNDef, err.Error(), timeout())
            return
        }
        pDataLen = n
        for err = packets.WriteDataPacket(conn, prevPacketRec, block, pData[:pDataLen], timeout()); err != nil ; {
            if retryCount < retry {
                retryCount++
                debug.Debugln("handleRead: Retrying packet send for block", block)
                err = packets.WriteDataPacket(conn, prevPacketRec, block, pData[:pDataLen], timeout())
            } else {
                packets.WriteErrorPacket(conn, prevPacketRec, tftpconst.ErrNDef, err.Error(), timeout())
                return 
            }
        }
        // Data packet sent. Wait for ack.
        pContent := make([]byte, pLen)
        for ackReceived := false; !ackReceived ; {
            (*conn).SetDeadline(timeout())
            n, src ,err := (*conn).ReadFrom(pContent)
            if err != nil {
                e, ok := err.(net.Error)
                if ok && e.Timeout() == true && retryCount < retry {
                    retryCount++
                    debug.Debugln("handleRead: timed out waiting for client. Retrying send for block", block)
                    packets.WriteDataPacket(conn, prevPacketRec, block, pData[:pDataLen], timeout())
                    continue
                }
                packets.WriteErrorPacket(conn, prevPacketRec, tftpconst.ErrNDef, err.Error(), timeout())
                return 
            }
            packetRec := &packets.SourcedPacket{pContent[:n], src}
            // Check sender TID correctness
            if (packetRec.TID() != p.TID()) {
                errMsg := fmt.Sprintf("Unrecognized sender TID %v", filename)
                packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrUnknownTID, errMsg, timeout())
                continue
            }
            // Check OpCode correctness
            if op, _ := packetRec.Opcode(); op != tftpconst.OpAck {
                errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
                packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrIllegalOp, errMsg, timeout())
                return
            } 
            // CHeck Block correctness (duplicate or next block)
            pBlock, err := packetRec.Block()
            if err != nil {
                packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, err.Error(), timeout())
                return
            }
            if pBlock < block {
                // This could be an old duplicate that was lost in the network. Ignoring. 
                continue;
            } else if pBlock != block {
                errMsg := fmt.Sprintf("Unexpected block # %v; was expecting %v", pBlock, block)
                packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, errMsg, timeout())
                return
            }
            
            ackReceived = true
            prevPacketRec = packetRec
        }
        block++
        retryCount = 0
    }
    debug.Debugln("handleRead: complete read transaction for file", filename)
    return
}

/*
 * Handle control flow for write transaction type
 */
func handleWrite(conn *net.PacketConn, p *packets.SourcedPacket, fs *InMemFS) {
    filename, tmode, err := p.ReqParams()
    if err != nil {
        packets.WriteErrorPacket(conn, p, tftpconst.ErrIllegalOp, err.Error(), timeout())
        return
    }
    if "octet" != strings.ToLower(tmode) {
        errMsg := fmt.Sprintf("Unsupported TFTP mode %v. Only octet mode is supported by this server.", tmode)
        packets.WriteErrorPacket(conn, p, tftpconst.ErrNDef, errMsg, timeout())
        return
    }
    file, exists := (*fs)[filename] 
    if exists {
        file.lock.Lock()
        defer file.lock.Unlock()
        if !overwriteEnabled && file.isFinished() {
            errMsg := fmt.Sprintf("A file already exists for filename: %v", filename)
            packets.WriteErrorPacket(conn, p, tftpconst.ErrFileExists, errMsg, timeout())
            return
        }
    } else {
        var lock sync.RWMutex
        (&lock).Lock()
        defer (&lock).Unlock()
        file = &InMemFile{&lock, []byte(nil)}
    }
    // put the unfinished file now to prevent duplicate writes
    // if this request errors out, the unfinished file will stay in the map until it is cleaned up.
    (*fs)[filename] = file
    
    newFileDataBuf := &bytes.Buffer{}
    pContent := make([]byte, pLen)
    block := uint16(0)
    prevPacketRec := p
    retryCount := 0
    
    // Write the first Ack. This block occurs a few times below and could probably be factored into a separate func
    for err:= packets.WriteAckPacket(conn, prevPacketRec, block, timeout()); err != nil ; {
        if retryCount < retry {
            retryCount++
            debug.Debugln("handleWrite: Retrying packet send for block", block)
            err = packets.WriteAckPacket(conn, prevPacketRec, block, timeout())
        } else {
            packets.WriteErrorPacket(conn, prevPacketRec, tftpconst.ErrNDef, err.Error(), timeout())
            return 
        }
    }
    
    for prevBytesWritten := blockSize; prevBytesWritten == blockSize ; {
        (*conn).SetDeadline(timeout())
        n, src ,err := (*conn).ReadFrom(pContent)
        if err != nil {
            e, ok := err.(net.Error)
            if ok && e.Timeout() == true && retryCount < retry {
                retryCount++
                debug.Debugln("handleWrite: timed out waiting for client. Retrying send for block", block)
                packets.WriteAckPacket(conn, prevPacketRec, block, timeout())
                continue
            }
            packets.WriteErrorPacket(conn, p, tftpconst.ErrNDef, err.Error(), timeout())
            return 
        }
        packetRec := &packets.SourcedPacket{pContent[:n], src}
        // Check sender TID correctness
        if (packetRec.TID() != p.TID()) {
            errMsg := fmt.Sprintf("Unrecognized sender TID %v", filename)
            packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrUnknownTID, errMsg, timeout())
            continue
        }
        // Check OpCode correctness
        if op, _ := packetRec.Opcode(); op != tftpconst.OpData {
            errMsg := fmt.Sprintf("Opcode %v is invalid in this context.", op)
            packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrIllegalOp, errMsg, timeout())
            return
        } 
        // CHeck Block correctness (duplicate or next block)
        pBlock, err := packetRec.Block()
        if err != nil {
            packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, err.Error(), timeout())
            return
        }
        if pBlock < block {
            // This could be an old duplicate that was lost in the network. Ignoring. 
            continue;
        }
        if pBlock == block {
            // don't reset retryCount before responding to duplicates 
            for err:= packets.WriteAckPacket(conn, packetRec, block, timeout()); err != nil ; {
                if retryCount < retry {
                    retryCount++
                    debug.Debugln("handleWrite: Retrying packet send for block", block)
                    err = packets.WriteAckPacket(conn, packetRec, block, timeout())
                } else {
                    packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, err.Error(), timeout())
                    return 
                }
            }
            continue;
        }
        block++
        if pBlock != block {
            errMsg := fmt.Sprintf("Unexpected block # %v; was expecting %v", pBlock, block)
            packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, errMsg, timeout())
            return
        }
        pdata, _ := packetRec.ContentData()
        bytesWritten, _ := newFileDataBuf.Write(pdata)
        prevBytesWritten = bytesWritten
        prevPacketRec = packetRec
        retryCount = 0
        for err:= packets.WriteAckPacket(conn, packetRec, block, timeout()); err != nil ; {
            if retryCount < retry {
                retryCount++
                debug.Debugln("handleWrite: Retrying packet send for block", block)
                err = packets.WriteAckPacket(conn, packetRec, block, timeout())
            } else {
                packets.WriteErrorPacket(conn, packetRec, tftpconst.ErrNDef, err.Error(), timeout())
                return 
            }
        }
    }
    debug.Debugln("handleWrite: Data transfer complete. Updating file data")
    file.data = newFileDataBuf.Bytes()
    // TODO: listen a little longer to make sure the client doesn't need us to re-ACK the last DATA packet
    return
}


/* 
 * main function performs some initialization tasks and then waits on console input
 */
func main() {
    switch opsys:= runtime.GOOS; opsys {
    case "windows":
        // Do nothing for now
    default:
        fmt.Printf("This application has not yet been tested on OS %s. You have been warned.\n", opsys)
    }
    // Handle args
    args := os.Args[1:]
    for _, arg := range args {
        switch(arg) {
            case "d":
                fallthrough
            case "-d":
                debug.Debug = true
                debug.Debugln("debug.Debug output is on.")
            case "--disableOverwrite":
                overwriteEnabled = false
            case "--disableLockOnRead":
                lockFileForRead = false
        }
    }
    debug.Debugln("overwriteEnabled =", overwriteEnabled, "; lockFileForRead =", lockFileForRead)
    
    fmt.Printf("Starting TFTP server...\n")
    // Initialize in-memory file system
    fs := make(InMemFS)
    // Since its just an in memory map, no need to defer any cleanup tasks
    
    // Start up request listener routine
    quit := make(chan bool)
    go listen(&fs, quit)
    
    // The main thread will listen for a 'shutdown' command on standard input
    for {
        var input string
        _, inputErr := fmt.Scanln(&input)
        if inputErr == nil {
            // handle input
            switch input {
                case "d":
                    debug.Debug = !debug.Debug
                    fmt.Println("Set debug.Debug mode: ", debug.Debug)
                case "ls":
                    fmt.Println(fs)
                case "shutdown":
                    quit <- true
                default:
                    debug.Debugln("Unrecognized console input",input)
            }
        } else {
            fmt.Println("Error reading console input: ", inputErr)
        }
        time.Sleep(1 * time.Second)
    }
}