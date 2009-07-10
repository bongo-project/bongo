#ifndef LOGGER_H
#define LOGGER_H

#ifndef LOGGERNAME
#define LOGGERNAME "root"
#endif

void Log(int level, char *message, ...);
void LogOpen(const char *agentname);
void LogClose();

// various macros
#define LogFailure(message) Log(LOG_ERROR, "Internal Failure (%s:%d) " message, __FILE__, __LINE__)
#define LogFailureF(message, ...) Log(LOG_ERROR, "Internal Failure (%s:%d) " message, __FILE__, __LINE__, __VA_ARGS__)
#define LogAssert(test, message) if (!(test)) { Log(LOG_ERROR, "Internal Failure (%s:%d) " message, __FILE__, __LINE__); }
#define LogAssertF(test, message, ...) if(!(test)) { Log(LOG_ERROR, "Assert:%s:%d " message, __FILE__, __LINE__, __VA_ARGS__); }
#define LogWithID(...) Log(__VA_ARGS__)

#define LOGIP(X)	inet_ntoa(X.sin_addr)
#define LOGIPPTR(X)	inet_ntoa(X->sin_addr)

// deprecated start up functions
#define LogStart()	LogOpen(LOGGERNAME);
#define LogStartup()	LogOpen(LOGGERNAME);
#define LogShutdown()	LogClose();

#define LOG_FATAL    10
#define LOG_ALERT    9
#define LOG_CRIT     8
#define LOG_CRITICAL 8
#define LOG_ERROR    7
#define LOG_WARN     6
#define LOG_WARNING  6
#define LOG_NOTICE   5
#define LOG_INFO     4
#define LOG_DEBUG    3
#define LOG_TRACE    2
#define LOG_NOTSET   1
#define LOG_UNKNOWN  0

#if 0
typedef struct _LoggingHandle LoggingHandle;

/* Prototypes */
EXPORT LoggingHandle *LoggerOpen(const char *name);
EXPORT void LoggerClose(LoggingHandle *handle);
EXPORT void LoggerEvent(LoggingHandle *handle, const char *subsystem, unsigned long eventId, int level, int unknown /*fixme*/, const char *str1, const char *str2, int i1, int i2, void *p, int size);
#endif 

#endif /* LOGGER_H */
