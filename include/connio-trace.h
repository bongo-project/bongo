
#if !defined(_BONGO_CONN_TRACE)

#define CONN_TRACE_GET_FLAGS()                      0
#define CONN_TRACE_SET_FLAGS(f) 
#define CONN_TRACE_INIT(p, n) 
#define CONN_TRACE_SHUTDOWN() 
#define CONN_TRACE_CREATE_DESTINATION(t)            NULL
#define CONN_TRACE_FREE_DESTINATION(d)
#define CONN_TRACE_BEGIN(c, t, d) 
#define CONN_TRACE_END(c) 
#define CONN_TRACE_EVENT(c, t) 
#define CONN_TRACE_ERROR(c, e, l) 
#define CONN_TRACE_DATA(c, t, b, l) 
#define CONN_TRACE_DATA_AND_ERROR(c, t, b, l, e) 

#else

void ConnTraceSetFlags(unsigned long flags);
unsigned long ConnTraceGetFlags(void);
void ConnTraceInit(char *path, char *name);
void ConnTraceShutdown(void);
TraceDestination *ConnTraceCreatePersistentDestination(unsigned char type);
void ConnTraceFreeDestination(TraceDestination *destination);
void ConnTraceBegin(Connection *c, unsigned long type, TraceDestination *destination);
void ConnTraceEnd(Connection *c);
void ConnTraceEvent(Connection *c, unsigned long event, char *buffer, long len);

#define CONN_TRACE_GET_FLAGS()                      ConnTraceGetFlags()
#define CONN_TRACE_SET_FLAGS(f)                     ConnTraceSetFlags(f)
#define CONN_TRACE_INIT(p, n)                       ConnTraceInit(p, n)
#define CONN_TRACE_SHUTDOWN()                       ConnTraceShutdown()
#define CONN_TRACE_CREATE_DESTINATION(t)            ConnTraceCreatePersistentDestination(t)
#define CONN_TRACE_FREE_DESTINATION(d)              ConnTraceFreeDestination(d) 
#define CONN_TRACE_BEGIN(c, t, d)                   ConnTraceBegin((c), (t), (d))
#define CONN_TRACE_END(c)                           ConnTraceEnd((c))
#define CONN_TRACE_EVENT(c, t)                      ConnTraceEvent((c), (t), NULL, 0)
#define CONN_TRACE_ERROR(c, e, l)                   ConnTraceEvent((c), CONN_TRACE_EVENT_ERROR, (e), (l))
#define CONN_TRACE_DATA(c, t, b, l)                 ConnTraceEvent((c), (t), (b), (l))
#define CONN_TRACE_DATA_AND_ERROR(c, t, b, l, e)    if ((l) > 0) { \
                                                        ConnTraceEvent((c), (t), (b), (l)); \
                                                    } else { \
                                                        ConnTraceEvent((c), CONN_TRACE_EVENT_ERROR, (e), (l)); \
                                                    }

#endif
