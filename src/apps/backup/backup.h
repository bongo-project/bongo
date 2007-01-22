/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
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

#ifndef BONGOBACKUP_H
#define BONGOBACKUP_H

#define DB_EXT ".db"
#define DATA_EXT ".data"

#define DOCSTATE_UNDEFINED 0
#define DOCSTATE_CREATED 1
#define DOCSTATE_MODIFIED 2
#define DOCSTATE_DELETED 3

#define QUERY_INIT_DB \
"CREATE TABLE document (document_id INTEGER PRIMARY KEY AUTOINCREMENT, history_id INTEGER, position INTEGER, length INTEGER);"\
"CREATE TABLE history (history_id INTEGER PRIMARY KEY AUTOINCREMENT, guid VARCHAR(47), event INTEGER, event_date INTEGER);"\
"CREATE TABLE property (property_id INTEGER PRIMARY KEY AUTOINCREMENT, history_id INTEGER, name VARCHAR(64), value TEXT);"

#define QUERY_PROP_VAL "SELECT p.value FROM history h, property p, (SELECT MAX(event_date) AS event_date FROM history h, property p WHERE h.history_id=p.history_id AND h.guid=? AND h.event_date<=? AND p.name=?) x WHERE h.history_id=p.history_id AND h.guid=? AND h.event_date=x.event_date AND p.name=?;"
#define QUERY_HIST_EVENT "SELECT event FROM history, (SELECT MAX(event_date) AS event_date FROM history WHERE guid=? AND event_date<=?) x WHERE history.guid=? AND history.event_date=x.event_date;"
#define QUERY_DOC_ATTRS "SELECT d.position, d.length FROM history h, document d, (SELECT MAX(event_date) AS event_date FROM history h, document d WHERE h.history_id=d.history_id AND h.guid=? AND h.event_date<=?) x WHERE h.history_id=d.history_id AND h.guid=? AND h.event_date=x.event_date;"
#define QUERY_ALL_PROPS "SELECT p.name, p.value FROM history h, property p, (SELECT name, MAX(event_date) AS event_date FROM history h, property p WHERE h.history_id=p.history_id AND h.guid=? AND h.event_date<=? GROUP BY p.name) x WHERE h.history_id=p.history_id AND h.guid=? AND p.name=x.name AND h.event_date=x.event_date AND p.value IS NOT NULL;"
#define QUERY_ALL_DOCS "SELECT h.guid FROM history h, (SELECT guid, MAX(event_date) AS event_date FROM history WHERE event_date<=? GROUP BY guid) x WHERE h.event_date=x.event_date AND h.guid=x.guid AND h.event<3;"
#define QUERY_DOCS_MOD "SELECT h.guid, p.value FROM history h, property p, (SELECT guid, MAX(event_date) AS event_date FROM history WHERE event_date<=? GROUP BY guid) x WHERE h.event_date=x.event_date AND h.guid=x.guid AND h.event<3 AND h.history_id=p.history_id AND p.name='nmap.modified';"

typedef struct {
    char *user;
    char *guids[256];
    int next_guid;
} BackupArg;

#endif