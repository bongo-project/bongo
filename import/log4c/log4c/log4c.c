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
#include "log4c.h"

void LogMsg(const char* loggerName, int id, int level, char* msg, ...)
{
    log4c_category_t* cat = log4c_category_get(loggerName);
    va_list va;
    if (log4c_category_is_priority_enabled(cat, level)) {
	va_start(va, msg);
	log4c_category_vlog(cat, id, level, msg, va);
	va_end(va);
    }
}
