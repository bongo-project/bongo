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
/*
 * Follow the bouncing comments in this file to see how Log4c can be used
 * within Bongo.  See also Makefile.am for setting up INCLUDES and _LDADD.
 * 
 * 
 * The Log4c library looks for logging configuration files in:
 * ${LOG4C_RCPATH}/log4crc
 * SYSCONFDIR/bongo/log4crc (/opt/bongo/etc. or similar)
 * ./log4crc
 * The example config file for this program has two categories defined,
 * "root" which is the parent for everything (all events bubble to it),
 * and "funtest", a category representing events from this code.
 * "root" is set to output to syslog, but only LOG_FATAL severity messages.
 * "funtest" outputs to stderr, any severity.
 * I would leave these notes in the config file, but Log4c's XML parser
 * seems to choke on comments.
 */

#define LOGGERNAME "funtest"
/*
 * recommended:
 *   define LOGGERNAME in your source file to categorize logging output
 *   alternately, specify a category during each logging call (see below)
 * default:
 *   LOGGERNAME will be "root" if you do not define it
 */

#include <stdio.h>
#include <log4c.h>
/*
 * required:
 *   include log4c.h for necessary logging functions
 */

int main(int argc, char** argv)
{
    int i = 0;

    /*
     * required:
     *   call LogStartup() before making any logging calls
     */
    LogStartup ();

    printf ("logging some one-liner events...\n");

    /*
     * simplest logging:
     *   Log (level, message, ...) implies LOGGERNAME as the category
     */
    Log (LOG_DEBUG, "this is a quick warning event");
    Log (LOG_DEBUG, "this is a quick warning event2");
    Log (LOG_DEBUG, "this is a quick warning event3");
    Log (LOG_DEBUG, "this is a quick warning event4");
    Log (LOG_DEBUG, "printf-style formatting %d %s", 123, "cool");

    /*
     * legacy-style logging:
     *   LogWithID (id, level, message, ...) uses event IDs for Novell Audit
     *   0 is a safe and meaningless event ID to pass in.
     *   (Audit will recieve such events as ID 0x0002ffff, an unspecified
     *    NetMail event)
     * Note about Audit logging:
     *   the first two string and first two numeric arguments get picked up
     *   and the format string gets disregarded when using the "audit"
     *   appender, with Log() as well as LogWithID().
     *   Any other appender disregards any ID and logs the formatted string.
     */
    #define NAUDIT_EVENT_MEANING_OF_LIFE 0x0002002A
    LogWithID (NAUDIT_EVENT_MEANING_OF_LIFE,
               LOG_FATAL,
               "Discovered the meaning of life: %s %s %d %d",
               "free Bongo",
               "free Beer",
               2006,
               2002);

    /*
     * LogMsg (category, id, level, message, ...) uses an expressed category
     */
    LogMsg ("root", 0, LOG_FATAL, "This is a quick event to root");
    LogMsg ("root", 0, LOG_ALERT, "brought to you by the number %d...", i);
    LogMsg ("root", 0, LOG_FATAL, "...and the letter %s", "H");

    printf("spinning through categories...\n");
    for (i = 0; i < LOG_UNKNOWN; i++) {
	Log(i*100, "this is a %s event", log4c_priority_to_string(i*100));
	/*
	 * log4c provides a bunch of other functions beyond the bongo-wrapped
	 * Log and LogMsg, if those are not detailed enough.  I won't get
	 * into those here though.
	 */
    }

    printf("quitting\n");

    LogShutdown();
    /*
     * required:
     *   call LogShutdown() when done logging
     * beware:
     *   if Log4c is outputting to stdout or stderr, the stream will be closed
     */

    return 0;
}
