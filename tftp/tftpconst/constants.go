// Package for some constants related to the TFTP protocol
package tftpconst

const (
    // server transaction listener ip address (the port for the initial request listener)
    Laddr = ":69"
    
    // op code constants
    OpRrq uint16 = 1
    OpWrq uint16 = 2
    OpData uint16 = 3
    OpAck uint16 = 4
    OpErr uint16 = 5
    
    // error code constants
    ErrNDef uint16 = 0
    ErrFNF uint16 = 1
    ErrAccess uint16 = 2
    ErrDiskFull uint16 = 3
    ErrIllegalOp uint16 = 4
    ErrUnknownTID uint16 = 5
    ErrFileExists uint16 = 6
)