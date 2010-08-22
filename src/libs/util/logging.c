#include <syslog.h>
#include <stdarg.h>

const int syslog_mapping[] = {
	LOG_DEBUG,	// UNKNOWN
	LOG_DEBUG,	// NOTSET
	LOG_DEBUG,	// TRACE
	LOG_DEBUG,	// DEBUG
	LOG_INFO,	// INFO
	LOG_NOTICE,	// NOTICE
	LOG_WARNING,	// WARNING
	LOG_ERR,	// ERROR
	LOG_CRIT,	// CRITICAL
	LOG_ALERT,	// ALERT
	LOG_EMERG	// FATAL
};

void
Log(int level, char *message, ...)
{
	va_list args;
	
	if ((level>10) || (level<0)) return;

#ifdef DEBUG
	// don't output TRACE or lower if DEBUG is on.
	if (level <= LOG_TRACE) return;
#else
	// don't output DEBUG or lower messages if DEBUG is not on
	if (level <= LOG_DEBUG) return;
#endif
	
	va_start(args, message);
	vsyslog(syslog_mapping[level], message, args);
	va_end(args);
}

void
LogOpen(const char *agentname)
{
	openlog(agentname, LOG_NDELAY, LOG_MAIL);
}

void
LogClose()
{
	closelog();
}
