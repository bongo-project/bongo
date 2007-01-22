#include <config.h>
#include <xpl.h>
#include <memmgr.h>

#include <connio.h>
#include <stdio.h>

#ifdef HAVE_SYS_SOCKET_H
#include <sys/socket.h>
#endif

#ifdef HAVE_NETINET_IN_H
#include <netinet/in.h>
#endif

#include <sys/un.h>

#include "conniop.h"

#if !defined(CONN_TRACE)
BOOL
ConnTraceAvailable(void)
{
    return(FALSE);
}
#else 
BOOL
ConnTraceAvailable(void)
{
    return(TRUE);
}

char *ConnTypeName[] = {
	"client", 
	"server", 
	"nmap" 
};

__inline static BOOL
ConnTraceTime(FILE *traceFile, unsigned long utcTime)
{
    struct tm *ltime;
	char tempBuf[80];

	tzset();
	ltime = localtime(&utcTime);
	strftime(tempBuf, 80, "%a, %d %b %Y %H:%M:%S", ltime);
	if (fprintf(traceFile, tempBuf) > -1) {
        return(TRUE);
    }
    return(FALSE);
}

unsigned long
ConnTraceGetFlags(void)
{
    return(ConnIO.trace.flags);
}

void
ConnTraceSetFlags(unsigned long flags)
{
    if (flags) {
        ConnIO.trace.enabled = TRUE;
        ConnIO.trace.flags = flags;
    } else {
        ConnIO.trace.enabled = FALSE;
        ConnIO.trace.flags = 0;
    }
}

void 
ConnTraceInit(char *path, char *name)
{
    unsigned long len;
    char *tracePath;
    char *ptr;

    tracePath = MemMalloc(XPL_MAX_PATH + 1);
    if (tracePath) {
        strcpy(tracePath, path);
        /* remove trailing slashes */
        do {
            len = strlen(tracePath);
            if (len > 0) {
                if ((tracePath[len - 1] == '\\') || (tracePath[len - 1] == '/')) {
                    tracePath[len - 1] = '\0';
                    continue;
                }
            }
            break;
        } while (TRUE);

        /* create a subdirectory for everytime the server is loaded */
        sprintf(tracePath + strlen(tracePath), "/%s/trace/%d", name, (int)(time(NULL)));
        
        /* create the path if it does not exist */
        ptr = tracePath;
        if (*ptr == '/') {
            ptr++;
        }

        do {
            ptr = strchr(ptr, '/');
            if (ptr) {
                *ptr = '\0';
                // FIXME: access()
                if (access(tracePath, 0) != 0) {
                    if (XplMakeDir(tracePath) != 0) {
                        break;
                    }
                }
                *ptr = '/';
                ptr++;
                continue;
            }

            // FIXME: access()
            if (access(tracePath, 0) != 0) {
                XplMakeDir(tracePath);
            }

            break;
        } while (TRUE);

        strcpy(ConnIO.trace.path, tracePath);
        MemFree(tracePath);
    }

    XplOpenLocalSemaphore(ConnIO.trace.nextIdSem, 1);
    ConnIO.trace.enabled = FALSE;
    ConnIO.trace.flags = 0;
    return;
}

void 
ConnTraceShutdown(void)
{
    rmdir(ConnIO.trace.path);

    XplCloseLocalSemaphore(ConnIO.trace.nextIdSem);

}

__inline unsigned long
ConnTraceGetConnectionFlags(unsigned long type)
{
    if (!ConnIO.trace.enabled) {
        return(0);
    } 

    switch (type) {
        case CONN_TYPE_INBOUND: {
            if (ConnIO.trace.flags & CONN_TRACE_SESSION_INBOUND) {
                return(ConnIO.trace.flags);
            }
            break;
        }

        case CONN_TYPE_OUTBOUND: {
            if (ConnIO.trace.flags & CONN_TRACE_SESSION_OUTBOUND) {
                return(ConnIO.trace.flags);
            }
            break;
        }

        case CONN_TYPE_NMAP: {
            if (ConnIO.trace.flags & CONN_TRACE_SESSION_NMAP) {
                return(ConnIO.trace.flags);
            }
            break;
        }
    }
    return(0);
}

void
ConnTraceFreeDestination(TraceDestination *destination)
{
    if (destination) {
        destination->useCount--;
        if (destination->useCount < 1) {
            if (destination->file) {
                fclose(destination->file);
            }
            MemFree(destination);
        }
    }
}

__inline static TraceDestination *
ConnTraceCreateDestination(void)
{
    char path[XPL_MAX_PATH];
    unsigned long id;
    TraceDestination *destination;

    destination = MemMalloc(sizeof(TraceDestination));
    if (destination) {
        destination->sequence = 1;
        XplWaitOnLocalSemaphore(ConnIO.trace.nextIdSem);
        ConnIO.trace.nextId++;
        id = ConnIO.trace.nextId;
        XplSignalLocalSemaphore(ConnIO.trace.nextIdSem);
        sprintf(path, "%s/%lu", ConnIO.trace.path, id);
        destination->file = fopen(path, "w");
        if (destination->file) {
            destination->useCount = 1;
            destination->startTime = time(NULL);
            if (fprintf(destination->file, "Trace Started ") > -1) {
                if (ConnTraceTime(destination->file, destination->startTime)) {
                    fwrite("\n", 1, 1, destination->file);
                    return(destination);
                }
            }
            fclose(destination->file);
        }
        MemFree(destination);
    }
    return(NULL);
}

TraceDestination *
ConnTraceCreatePersistentDestination(unsigned char type)
{
    if (ConnTraceGetConnectionFlags(type)) {
        return(ConnTraceCreateDestination());
    }

    return(NULL);
}

void
ConnTraceBegin(Connection *c, unsigned long type, TraceDestination *destination)
{
    c->trace.flags = ConnTraceGetConnectionFlags(type);

    if (c->trace.flags == 0) {
        return;
    }

    snprintf(c->trace.address, sizeof(c->trace.address), "%d.%d.%d.%d", c->socketAddress.sin_addr.s_net, c->socketAddress.sin_addr.s_host, c->socketAddress.sin_addr.s_lh, c->socketAddress.sin_addr.s_impno);
    c->trace.type = type;
    c->trace.typeName = ConnTypeName[c->trace.type];

    if (!destination) {
        if (!(c->trace.destination)) {
            c->trace.destination = ConnTraceCreateDestination();
            return;
        }

        ConnTraceFreeDestination(c->trace.destination);
        c->trace.destination = ConnTraceCreateDestination();
        return;
    }

    destination->useCount++;
    c->trace.destination = destination;

    return;
}

__inline static long
ConnTraceFormatHeader(Connection *c, char *message, char direction, long len)
{
    return(fprintf(c->trace.destination->file, 
                   "\n\n\n%lu %s %s(%s)  soc: %d  ssl: %s  age: %ld  len: %ld\n%c----------------------------------------------------------------------------%c\n", 
                   c->trace.destination->sequence++, 
                   message, 
                   c->trace.typeName, 
                   c->trace.address, 
                   c->socket,
                   (c->ssl.enable) ? "yes" : "no",
                   (long)(time(NULL) - c->trace.destination->startTime), 
                   len,
                   direction, 
                   direction));
}


__inline static long
ConnTraceFormatTrailer(Connection *c, char direction)
{
    return(fprintf(c->trace.destination->file, "\n%c----------------------------------------------------------------------------%c\n", direction, direction));
}

void
ConnTraceEnd(Connection *c)
{
    c->trace.flags = 0;
    ConnTraceFreeDestination(c->trace.destination);
    c->trace.destination = NULL;
    return;
}


void
ConnTraceEvent(Connection *c, unsigned long event, char *buffer, long len)
{
    if (!(c->trace.flags & event)) {
        return;
    }

    if (c->trace.destination) {
        switch (event) {
            case CONN_TRACE_EVENT_READ: {
                if (ConnTraceFormatHeader(c, "READ from", '<', len) > -1) {
                    if (fwrite(buffer, len, 1, c->trace.destination->file) == 1) {
                        if (ConnTraceFormatTrailer(c, '<') > -1) {
                            return;
                        }
                    }
                }
                break;
            }

            case CONN_TRACE_EVENT_WRITE: {
                if (ConnTraceFormatHeader(c, "WRITE to", '>', len) > -1) {
                    if (fwrite(buffer, len, 1, c->trace.destination->file) == 1) {
                        if (ConnTraceFormatTrailer(c, '>') > -1) {
                            return;
                        }
                    }
                }
                break;
            }

            case CONN_TRACE_EVENT_CONNECT: {
                if (c->trace.type == CONN_TYPE_INBOUND) {
                    if (ConnTraceFormatHeader(c, "ACCEPT connection from", '-', 0) > -1) {
                        return;
                    }
                    break;
                }

                if (ConnTraceFormatHeader(c, "CONNECT to", '-', 0) > -1) {
                    return;
                }
                break;
            }

            case CONN_TRACE_EVENT_CLOSE: {
                if (ConnTraceFormatHeader(c, "CLOSE", '-', 0) > -1) {
                    return;
                }
                break;
            }

            case CONN_TRACE_EVENT_SSL_CONNECT: {
                if (c->trace.type == CONN_TYPE_INBOUND) {
                    if (ConnTraceFormatHeader(c, "SSL_ACCEPT connection from", '-', 0) > -1) {
                        return;
                    }
                    break;
                }

                if (ConnTraceFormatHeader(c, "SSL_CONNECT to", '-', 0) > -1) {
                    return;
                }
                break;
            }

            case CONN_TRACE_EVENT_SSL_SHUTDOWN: {
                if (ConnTraceFormatHeader(c, "SSL_SHUTDOWN on", '-', 0) > -1) {
                    return;
                }
                break;
            }

            case CONN_TRACE_EVENT_ERROR: {
                if (ConnTraceFormatHeader(c, "ERROR", '#', 0) > -1) {
                    /* for this event len contains the return value of the function that failed. NOT the length of the buffer */
                    if (fprintf(c->trace.destination->file, "%s returned %ld errno: %d", buffer, len, errno) > -1) {
                        if (ConnTraceFormatTrailer(c, '#') > -1) {
                            return;
                        }
                    }
                }
                break;
            }
        }
    }
    ConnTraceEnd(c);
    return;
}

#endif /* defined(CONN_TRACE) */
