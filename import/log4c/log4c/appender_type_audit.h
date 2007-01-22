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

#ifndef log4c_appender_type_audit_h
#define log4c_appender_type_audit_h

/**
 * @file appender_type_syslog.h
 *
 * @brief Log4c syslog(3) appender interface.
 *
 * The audit appender uses the Novell Audit interface for logging. The log4c
 * priorities are mapped to the Audit priorities and the appender name is
 * used as an Audit identifier. 1 default Audit appender is defined: @c
 * "audit".
 *
 * The following examples shows how to define and use audit appenders.
 * 
 * @code
 *
 * log4c_appender_t* myappender;
 *
 * myappender = log4c_appender_get("myappender");
 * log4c_appender_set_type(myappender, &log4c_appender_type_audit);
 * 
 * @endcode
 *
 **/

#include <log4c/defs.h>
#include <log4c/appender.h>

__LOG4C_BEGIN_DECLS

/**
 * Audit appender type definition.
 *
 * This should be used as a parameter to the log4c_appender_set_type()
 * routine to set the type of the appender.
 *
 **/
extern const log4c_appender_type_t log4c_appender_type_audit;

__LOG4C_END_DECLS

#endif
