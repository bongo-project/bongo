static const char version[] = "$Id: appender_type_syslog.c,v 1.5 2005/12/13 20:48:36 legoater Exp $";

/*
 * appender_syslog.c
 *
 * Copyright 2001-2003, Meiosys (www.meiosys.com). All rights reserved.
 *
 * See the COPYING file for the terms of usage and distribution.
 */

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif

#include <log4c/appender.h>
#include <log4c/priority.h>

#ifdef HAVE_SYSLOG_H
#include <syslog.h>

/*******************************************************************************/
static int log4c_to_syslog_priority(int a_priority)
{
    static int priorities[] = {
	LOG_EMERG,	
	LOG_ALERT,	
	LOG_CRIT, 
	LOG_ERR,	
	LOG_WARNING,	
	LOG_NOTICE, 
	LOG_INFO, 	
	LOG_DEBUG 
    };
    int result;
    
    a_priority++;
    a_priority /= 100;
    
    if (a_priority < 0) {
	result = LOG_EMERG;
    } else if (a_priority > 7) {
	result = LOG_DEBUG;
    } else {
	result = priorities[a_priority];
    }
    
    return result;
}

/*******************************************************************************/
static int syslog_open(log4c_appender_t* this)
{
    openlog(log4c_appender_get_name(this), LOG_PID, LOG_USER); 
    return 0;
}

/*******************************************************************************/
static int syslog_append(log4c_appender_t*	this, 
			 const log4c_logging_event_t* a_event)
{
    char *token;
    int string_len;
    char *stuffed_string;

    // we want to remove any '%' formatting characters from the
    // log string, since syslog will try to interpolate them

    // break string where '%' occurs, and append those tokens 
    // to the new string with '%%' following
    string_len = strlen(a_event->evt_rendered_msg);
    stuffed_string = malloc((string_len*2) + 1);
    if (stuffed_string == NULL) {
        // out of memory 
        return -1;
    }
    token = strtok(a_event->evt_rendered_msg, "%");
    strcpy(stuffed_string, token);
    while(NULL != (token = strtok(NULL, "%"))) {
        strcat(stuffed_string, "%%");
        strcat(stuffed_string, token);
    }

    syslog(log4c_to_syslog_priority(a_event->evt_priority) | LOG_USER, 
	   stuffed_string);
    free(stuffed_string);
    return 0;
}

/*******************************************************************************/
static int syslog_close(log4c_appender_t*	this)
{
    closelog();
    return 0;
}

#else

/*******************************************************************************/
static int syslog_open(log4c_appender_t* this)
{
    return 0;
}

/*******************************************************************************/
static int syslog_append(log4c_appender_t*	this, 
			 const log4c_logging_event_t* a_event)
{
    return 0;
}

/*******************************************************************************/
static int syslog_close(log4c_appender_t*	this)
{
    return 0;
}
#endif


/*******************************************************************************/
const log4c_appender_type_t log4c_appender_type_syslog = {
    "syslog",
    syslog_open,
    syslog_append,
    syslog_close,
};

/*
log4c_appender_type_define(log4c_appender_type_syslog);
*/
