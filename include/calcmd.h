/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/



#ifndef _CALCMD_H
#define _CALCMD_H

#include <inttypes.h>
#include <bongocal.h>

typedef enum _CalCmdCommandType
{
    CALCMD_ERROR = 0,
    CALCMD_QUERY,
    CALCMD_QUERY_CONFLICTS,
    CALCMD_NEW_APPT,
    CALCMD_HELP,
} CalCmdCommandType;

typedef struct _CalCmdCommand
{
    CalCmdCommandType type;

    BongoCalTime begin;
    BongoCalTime end;

    char data[128];
} CalCmdCommand;

CalCmdCommand *CalCmdNew(char *command, BongoCalTimezone *tz);
void CalCmdFree(CalCmdCommand *cmd);

int CalCmdParseCommand (char *buf, CalCmdCommand *cmd, BongoCalTimezone *tz);

BongoCalObject *CalCmdProcessNewAppt(CalCmdCommand *cmd, char *uid);
int CalCmdProcessQuery (CalCmdCommand *cmd, char *buf, size_t size);

int CalCmdSetDebugMode (int flag);
int CalCmdSetVerboseDebugMode (int flag);
time_t CalCmdSetTestMode (time_t basetime);


int32_t CalCmdPrintConciseDateTimeRange(CalCmdCommand *command, 
                                        char *buffer, size_t size);

int32_t CalCmdGetType(CalCmdCommand *cmd);
void CalCmdSetType(CalCmdCommand *cmd, int32_t value);
char * CalCmdGetData(CalCmdCommand *cmd);
void CalCmdSetData(CalCmdCommand *cmd, char *data);
int64_t CalCmdGetBegin(CalCmdCommand *cmd);
int64_t CalCmdGetEnd(CalCmdCommand *cmd);


/* FIXME: temporary */
void CalCmdLocalToUtc(struct tm *val, struct tm *valOut);
void CalCmdUtcToLocal(struct tm *val, struct tm *valOut);

#endif /* _CALCMD_H */
