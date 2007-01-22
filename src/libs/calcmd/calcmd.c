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

#include <config.h>
#include <xpl.h>
#include <memmgr.h>

#include <calcmd.h>
#include <bongocal.h>

BongoCalObject *
CalCmdProcessNewAppt(CalCmdCommand *cmd, char *uid)
{
    BongoCalObject *cal;
    BongoCalInstance *inst;
    
    cal = BongoCalObjectNew(NULL);

    inst = BongoCalInstanceNew(cal, NULL);
    
    BongoCalInstanceSetUid(inst, uid);
    BongoCalInstanceSetStart(inst, cmd->begin);
    BongoCalInstanceSetEnd(inst, cmd->end);
    BongoCalInstanceSetSummary(inst, cmd->data);

    BongoCalObjectAddInstance(cal, inst);
    
    return cal;
}


int
CalCmdPrintConciseDateTimeRange(CalCmdCommand *cmd, char *buffer, size_t size)
{
    int len = 0;

    len += BongoCalTimeToStringConcise(cmd->begin, buffer, size);
    if ((unsigned int)len >= size) {
        buffer[size-1] = 0;
        return size;
    }

    if (0) {
        /* FIXME: make this code more intelligent.  i.e. print "fri 2-3pm" instead
           of the wordy "fri 2pm - fri 3pm" */
        
        len += snprintf(buffer + len, size - len, " - ");
        if ((unsigned int)len >= size) {
            buffer[size-1] = 0;
            return size;
        }
        
        len += BongoCalTimeToStringConcise(cmd->end, buffer + len, size - len);
        if ((unsigned int)len >= size) {
            buffer[size-1] = 0;
            return size;
        }
    }

    return len;
}



/* these functions provided for the benefit of mono code */


int32_t
CalCmdGetType(CalCmdCommand *cmd) 
{
    return cmd->type;
}


void 
CalCmdSetType(CalCmdCommand *cmd, int32_t value)
{
    cmd->type = value;
}


char *
CalCmdGetData(CalCmdCommand *cmd)
{
    return cmd->data;
}

void 
CalCmdSetData(CalCmdCommand *cmd, char *data)
{
    strncpy (cmd->data, data, sizeof(cmd->data));
    cmd->data[sizeof(cmd->data)-1] = 0;
}


int64_t
CalCmdGetBegin(CalCmdCommand *cmd)
{
    return BongoCalTimeAsUint64(cmd->begin);
}


int64_t
CalCmdGetEnd(CalCmdCommand *cmd)
{
    return BongoCalTimeAsUint64(cmd->end);
}

CalCmdCommand *
CalCmdNew(char *command, BongoCalTimezone *tz)
{
    CalCmdCommand *cmd = MemMalloc(sizeof(CalCmdCommand));
    if (CalCmdParseCommand(command, cmd, tz)) {
        return cmd;
    }

    return NULL;
}

void
CalCmdFree(CalCmdCommand *cmd)
{
    MemFree(cmd);
}
