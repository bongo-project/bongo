/****************************************************************************
 *
 * Copyright (c) 2005 Novell, Inc.
 * All Rights Reserved.
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2.1 of the GNU Lesser General Public
 * License as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with this program; if not, contact Novell, Inc.
 *
 * To contact Novell about this file by physical or electronic mail,
 * you may find current contact information at www.novell.com
 *
 ****************************************************************************/

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif

#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <log4c/appender.h>
#include <log4c/priority.h>

#ifdef HAVE_LIBLOGEVENT
#include "audit_logevent.h"

static LOGHANDLE audit_handle;

/*******************************************************************************/
static int log4c_to_audit_priority(int a_priority)
{
    static int priorities[] = {
	LE_EMERGENCY,
	LE_ALERT,
	LE_CRITICAL,
	LE_ERROR,
	LE_WARNING,	
	LE_NOTICE, 
	LE_INFO, 	
	LE_DEBUG 
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

/*
 * readfile - read a text file into a string. 
 * note: returned string must be free()d later
 */
static unsigned char* readfile(char* filename)
{
    int filehandle = 0;
    int filepos = 0;
    int pos = 0;
    int size = 0;
    unsigned char* string = NULL;
    filehandle = open (filename, O_RDONLY);
    if (filehandle) {
        do {
            size += 10;
            string = realloc (string, size * sizeof (*string));
            pos = read (filehandle, string + (filepos * sizeof (*string)), 9);
            filepos += pos;
        } while(pos > 0);
        string[filepos] = '\0';
        close(filehandle);
    }
    return string;
}

/*******************************************************************************/
static int audit_open(log4c_appender_t* this)
{
    char filename[256];
    unsigned char* cert = NULL;
    unsigned char* pkey = NULL;
    snprintf (filename, sizeof (filename) - 1, "%s/bongocert.pem",
              getenv("LOG4C_RCPATH") ? getenv("LOG4C_RCPATH") : LOG4C_RCPATH);
    cert = readfile(filename);
    snprintf (filename, sizeof (filename) - 1, "%s/bongopkey.pem",
              getenv("LOG4C_RCPATH") ? getenv("LOG4C_RCPATH") : LOG4C_RCPATH);
    pkey = readfile(filename);
    audit_handle = LogOpen (cert, pkey, 0, NULL);
    free (cert);
    free (pkey);
    return 0;
}

/*******************************************************************************/
static int audit_append(log4c_appender_t* this, 
                        const log4c_logging_event_t* a_event)
{
    LogEventDirect (audit_handle,
                    (unsigned char*)a_event->evt_category,
                    a_event->evt_audit_id ?
                    a_event->evt_audit_id : NAUDIT_EVENT_NETMAIL_UNSPECIFIED,
                    log4c_to_audit_priority(a_event->evt_priority),
                    0,
                    0,
                    (unsigned char*)a_event->evt_audit_s1,
                    (unsigned char*)a_event->evt_audit_s2,
                    a_event->evt_audit_d1,
                    a_event->evt_audit_d2,
                    0,
                    0,
                    NULL);
    return 0;
}

/*******************************************************************************/
static int audit_close(log4c_appender_t* this)
{
    LogClose (audit_handle);
    return 0;
}

#else

/*******************************************************************************/
static int audit_open(log4c_appender_t* this)
{
    return 0;
}

/*******************************************************************************/
static int audit_append(log4c_appender_t* this, 
                         const log4c_logging_event_t* a_event)
{
    return 0;
}

/*******************************************************************************/
static int audit_close(log4c_appender_t*	this)
{
    return 0;
}
#endif


/*******************************************************************************/
const log4c_appender_type_t log4c_appender_type_audit = {
    "audit",
    audit_open,
    audit_append,
    audit_close,
};

/*
log4c_appender_type_define(log4c_appender_type_syslog);
*/
