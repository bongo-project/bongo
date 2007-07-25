/* $Id: log4c.h,v 1.3 2003/09/12 21:06:45 legoater Exp $
 *
 * log4c.h
 *
 * Copyright 2001-2002, Meiosys (www.meiosys.com). All rights reserved.
 *
 * See the COPYING file for the terms of usage and distribution.
 */
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

#ifndef bongo_log4c_h
#define bongo_log4c_h

#include <stdarg.h>
#include <log4c/version.h>
#include <log4c/init.h>
#include <log4c/rc.h>
#include <log4c/appender.h>
#include <log4c/category.h>
#include <log4c/layout.h>
#include <log4c/logging_event.h>
#include <log4c/priority.h>


#ifndef LOGGERNAME
#define LOGGERNAME "root"
#endif

#define LOG_FATAL    LOG4C_PRIORITY_FATAL
#define LOG_ALERT    LOG4C_PRIORITY_ALERT
#define LOG_CRIT     LOG4C_PRIORITY_CRIT
#define LOG_CRITICAL LOG4C_PRIORITY_CRIT
#define LOG_ERROR    LOG4C_PRIORITY_ERROR
#define LOG_WARN     LOG4C_PRIORITY_WARN
#define LOG_WARNING  LOG4C_PRIORITY_WARN
#define LOG_NOTICE   LOG4C_PRIORITY_NOTICE
#define LOG_INFO     LOG4C_PRIORITY_INFO
#define LOG_DEBUG    LOG4C_PRIORITY_DEBUG
#define LOG_TRACE    LOG4C_PRIORITY_TRACE
#define LOG_NOTSET   LOG4C_PRIORITY_NOTSET
#define LOG_UNKNOWN  LOG4C_PRIORITY_UNKNOWN

#define LOGIP(X) inet_ntoa(X.sin_addr)

#define LogAssert(test, message, ...) if((test)) { Log(LOG_ERROR, "Assert:%s:%d " message, __FILE__, __LINE__, __VA_ARGS__); }
#define Log(...) LogMsg(LOGGERNAME, 0, __VA_ARGS__)
#define LogWithID(...) LogMsg(LOGGERNAME, __VA_ARGS__)
#define LogStart() LoggerOpen(LOGGERNAME);
#define LogStartup() log4c_init()
#define LogShutdown() log4c_fini()


void LogMsg(const char*, int, int, char*, ...);


#endif

