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

#include "config.h"
#include <xpl.h>
#include <nmlib.h>
#include <sqlite3.h>
#include <time.h>

#include "backup.h"

#define malloc(bytes) MemMalloc(bytes)
#define free(ptr) MemFree(ptr)

#include <libintl.h>
#define _(x) gettext(x)

/* Globals */
struct {
    char *host_addr;
    char *store_path;
    char *auth_password;
    char *auth_username;
    unsigned long backup_date;
    int verbose;

    struct {
        sqlite3_stmt *stmt_prop_val;
        sqlite3_stmt *stmt_hist_event;
        sqlite3_stmt *stmt_doc_attrs;
        sqlite3_stmt *stmt_doc_info;
    } sql;
} BongoBackup;

int
OpenDatabase(char *db_filename, sqlite3 **db, int create_if_absent)
{
    int result = 1;
    FILE *db_file;

    /* check to see if database file already exists */
    if ((db_file = fopen(db_filename, "r"))) {
        if (fclose(db_file)) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't close file (%s)\n"), db_filename);
            }
            result = 0;
        }

        if (sqlite3_open(db_filename, db)) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't open user database\n"));
            }
            result = 0;
        }
    } else {
        if (create_if_absent) {
            if (!sqlite3_open(db_filename, db)) {
                if (sqlite3_exec(*db, QUERY_INIT_DB, NULL, NULL, NULL) != SQLITE_OK) {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("Failed to initialize database file\n"));
                    }

                    result = 0;
                }
            } else {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Couldn't open user database\n"));
                }

                result = 0;
            }
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("User database does not exist\n"));
            }

            result = 0;
        }
    }

    return result;
}

int
CloseDatabase(sqlite3 *db)
{
    int result = 1;

    if (BongoBackup.sql.stmt_prop_val) {
        if (sqlite3_finalize(BongoBackup.sql.stmt_prop_val) == SQLITE_OK) {
            BongoBackup.sql.stmt_prop_val = NULL;
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_PROP_VAL)\n"));
            }

            result = 0;
        }
    }

    if (BongoBackup.sql.stmt_hist_event) {
        if (sqlite3_finalize(BongoBackup.sql.stmt_hist_event) == SQLITE_OK) {
            BongoBackup.sql.stmt_hist_event = NULL;
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_HIST_EVENT)\n"));
            }

            result = 0;
        }
    }

    if (BongoBackup.sql.stmt_doc_attrs) {
        if (sqlite3_finalize(BongoBackup.sql.stmt_doc_attrs) == SQLITE_OK) {
            BongoBackup.sql.stmt_doc_attrs = NULL;
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_DOC_ATTRS)\n"));
            }

            result = 0;
        }
    }

    if (BongoBackup.sql.stmt_doc_info) {
        if (sqlite3_finalize(BongoBackup.sql.stmt_doc_info) == SQLITE_OK) {
            BongoBackup.sql.stmt_doc_info = NULL;
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_DOC_INFO)\n"));
            }

            result = 0;
        }
    }

    if (sqlite3_close(db) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't close database\n"));
        }

        result = 0;
    }

    return result;
}

int
SelectPropertyValue(sqlite3 *db, char *guid, char *property, unsigned long date, char **value)
{
    /* Don't forget to free() the 'value' afterwards */
    int result = 0;
    int rc;

    if (!BongoBackup.sql.stmt_prop_val) {
        if (sqlite3_prepare(db, QUERY_PROP_VAL, -1, &BongoBackup.sql.stmt_prop_val, 0) != SQLITE_OK) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't create prepared statement (QUERY_PROP_VAL)\n"));
            }
            return -1;
        }
    }

    if (sqlite3_bind_text(BongoBackup.sql.stmt_prop_val, 1, guid, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_int(BongoBackup.sql.stmt_prop_val, 2, date) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_prop_val, 3, property, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_prop_val, 4, guid, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_prop_val, 5, property, -1, SQLITE_STATIC) != SQLITE_OK) {

        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't bind to prepared statement (QUERY_PROP_VAL)\n"));
        }
        return -1;
    }

    if ((rc = sqlite3_step(BongoBackup.sql.stmt_prop_val)) == SQLITE_ROW) {
        if (value) {
            *value = malloc(sqlite3_column_bytes(BongoBackup.sql.stmt_prop_val, 0) + 1);

            if (!*value) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Insufficient memory available\n"));
                }
                return -1;
            }

            strcpy(*value, sqlite3_column_text(BongoBackup.sql.stmt_prop_val, 0));
        }

        result = 1;
    } else if (rc != SQLITE_DONE) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Query failed to execute (QUERY_PROP_VAL)\n"));
        }
        return -1;
    }

    if (sqlite3_reset(BongoBackup.sql.stmt_prop_val) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't reset prepared statement (QUERY_PROP_VAL)\n"));
        }
        return -1;
    }

    return result;
}

int
SelectDocumentAttributes(sqlite3 *db, char *guid, unsigned long date, int *len, int *pos)
{
    int result = 0;
    int rc;

    if (!BongoBackup.sql.stmt_doc_attrs) {
        if (sqlite3_prepare(db, QUERY_DOC_ATTRS, -1, &BongoBackup.sql.stmt_doc_attrs, 0) != SQLITE_OK) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't create prepared statement (QUERY_DOC_ATTRS)\n"));
            }
            return -1;
        }
    }

    if (sqlite3_bind_text(BongoBackup.sql.stmt_doc_attrs, 1, guid, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_int(BongoBackup.sql.stmt_doc_attrs, 2, date) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_doc_attrs, 3, guid, -1, SQLITE_STATIC) != SQLITE_OK) {

        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't bind to prepared statement (QUERY_DOC_ATTRS)\n"));
        }
        return -1;
    }

    if ((rc = sqlite3_step(BongoBackup.sql.stmt_doc_attrs)) == SQLITE_ROW) {
        if (pos) {
            *pos = sqlite3_column_int(BongoBackup.sql.stmt_doc_attrs, 0);
        }

        if (len) {
            *len = sqlite3_column_int(BongoBackup.sql.stmt_doc_attrs, 1);
        }

        result = 1;
    } else if (rc != SQLITE_DONE) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Query failed to execute (QUERY_DOC_ATTRS)\n"));
        }
        return -1;
    }

    if (sqlite3_reset(BongoBackup.sql.stmt_doc_attrs) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't reset prepared statement (QUERY_DOC_ATTRS)\n"));
        }
        return -1;
    }

    return result;
}

int
SelectDocumentInfo(sqlite3 *db, char *guid, unsigned long date, char *resource, int *type, int *length)
{
    int result = 0;
    int rc;
    int found_resource = 0;
    int found_type = 0;
    int found_length = 0;

    if (!BongoBackup.sql.stmt_doc_info) {
        if (sqlite3_prepare(db, QUERY_ALL_PROPS, -1, &BongoBackup.sql.stmt_doc_info, 0) != SQLITE_OK) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't create prepared statement (QUERY_ALL_PROPS)\n"));
            }
            return -1;
        }
    }

    if (sqlite3_bind_text(BongoBackup.sql.stmt_doc_info, 1, guid, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_int(BongoBackup.sql.stmt_doc_info, 2, date) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_doc_info, 3, guid, -1, SQLITE_STATIC) != SQLITE_OK) {

        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't bind to prepared statement (QUERY_ALL_PROPS)\n"));
        }
        return -1;
    }

    while ((rc = sqlite3_step(BongoBackup.sql.stmt_doc_info)) == SQLITE_ROW) {
        if (!strcmp(sqlite3_column_text(BongoBackup.sql.stmt_doc_info, 0), "nmap.resource")) {
            if (resource) {
                strcpy(resource, sqlite3_column_text(BongoBackup.sql.stmt_doc_info, 1));
            }

            found_resource = 1;
        } else if (!strcmp(sqlite3_column_text(BongoBackup.sql.stmt_doc_info, 0), "nmap.type")) {
            if (type) {
                *type = sqlite3_column_int(BongoBackup.sql.stmt_doc_info, 1);
            }

            found_type = 1;
        } else if (!strcmp(sqlite3_column_text(BongoBackup.sql.stmt_doc_info, 0), "nmap.length")) {
            if (length) {
                *length = sqlite3_column_int(BongoBackup.sql.stmt_doc_info, 1);
            }

            found_length = 1;
        }
    }

    if (rc != SQLITE_DONE) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Query failed to execute (QUERY_ALL_PROPS)\n"));
        }
        return -1;
    }

    if (found_resource && found_type && found_length) {
        result = 1;
    }

    if (sqlite3_reset(BongoBackup.sql.stmt_doc_attrs) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't reset prepared statement (QUERY_ALL_PROPS)\n"));
        }
        return -1;
    }

    return result;
}

int
SelectHistoricEvent(sqlite3 *db, char *guid, unsigned long date, int *event)
{
    int result = 0;
    int rc;

    if (!BongoBackup.sql.stmt_hist_event) {
        if (sqlite3_prepare(db, QUERY_HIST_EVENT, -1, &BongoBackup.sql.stmt_hist_event, 0) != SQLITE_OK) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't create prepared statement (QUERY_HIST_EVENT)\n"));
            }
            return -1;
        }
    }

    if (sqlite3_bind_text(BongoBackup.sql.stmt_hist_event, 1, guid, -1, SQLITE_STATIC) != SQLITE_OK ||
        sqlite3_bind_int(BongoBackup.sql.stmt_hist_event, 2, date) != SQLITE_OK ||
        sqlite3_bind_text(BongoBackup.sql.stmt_hist_event, 3, guid, -1, SQLITE_STATIC) != SQLITE_OK) {

        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't bind to prepared statement (QUERY_HIST_EVENT)\n"));
        }
        return -1;
    }

    if ((rc = sqlite3_step(BongoBackup.sql.stmt_hist_event)) == SQLITE_ROW) {
        if (event) {
            *event = sqlite3_column_int(BongoBackup.sql.stmt_hist_event, 0);
        }

        result = 1;
    } else if (rc != SQLITE_DONE) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Query failed to execute (QUERY_HIST_EVENT)\n"));
        }
        return -1;
    }

    if (sqlite3_reset(BongoBackup.sql.stmt_hist_event) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't reset prepared statement (QUERY_HIST_EVENT)\n"));
        }
        return -1;
    }

    return result;
}

int
InsertHistory(sqlite3 *db, const char *guid, int event, unsigned long date)
{
    int id = 0;
    char *qstr = "INSERT INTO history (history_id, guid, event, event_date) VALUES (NULL, '%s', %d, %d);";
    char qbuf[256];

    sprintf(qbuf, qstr, guid, event, date);

    if (sqlite3_exec(db, qbuf, NULL, NULL, NULL) == SQLITE_OK) {
        id = sqlite3_last_insert_rowid(db);
    }

    return id;
}

int
InsertProperty(sqlite3 *db, int history, char *property, char *value)
{
    int id = 0;
    char *qstr = "INSERT INTO property (property_id, history_id, name, value) VALUES (NULL, %d, '%s', '%s');";
    int qsize = strlen(qstr) + 32 + strlen(property) + strlen(value) + 1; 
    char *qbuf = malloc(qsize);

    sprintf(qbuf, qstr, history, property, value);

    if (sqlite3_exec(db, qbuf, NULL, NULL, NULL) == SQLITE_OK) {
        id = sqlite3_last_insert_rowid(db); 
    }

    free(qbuf);
    return id;
}

int
InsertDocument(sqlite3 *db, int history, int length, int position)
{
    int id = 0;
    char *qstr = "INSERT INTO document (document_id, history_id, position, length) VALUES (NULL, %d, %d, %d);";
    char qbuf[256];

    sprintf(qbuf, qstr, history, position, length);

    if (sqlite3_exec(db, qbuf, NULL, NULL, NULL) == SQLITE_OK) {
        id = sqlite3_last_insert_rowid(db);
    }

    return id;
}

Connection *
GetConnection(char *user)
{
    Connection *nmap;
    char cmd[1024];
    char buf[1024];
    char *ptr;
    int user_exists = FALSE;
    int code;

    if (!(nmap = NMAPConnect(BongoBackup.host_addr, NULL))) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Connection to host %s failed\n"), BongoBackup.host_addr);
        }

        return NULL;
    }

    if (!ConnReadLine(nmap, buf, sizeof(buf))) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Connection to host %s failed"), BongoBackup.host_addr);
        }

        NMAPQuit(nmap);
        return NULL;
    }

    sprintf(cmd, "PASS USER %s %s\r\n", BongoBackup.auth_username, BongoBackup.auth_password);
    NMAPSendCommand(nmap, cmd, strlen(cmd));
    if (NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE) != 1000) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("NMAP command failed (PASS): %s\n"), buf);
        }

        NMAPQuit(nmap);
        return NULL;
    }

    NMAPSendCommand(nmap, "MANAGE\r\n", 8);
    if (NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE) != 1000) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("NMAP command failed (MANAGE): %s\n"), buf);
        }

        NMAPQuit(nmap);
        return NULL;
    }

    if (user) {
        /* make sure user exists on host before issuing USER command */
        /* (USER will create the account if it doesn't already exist) */
        sprintf(cmd, "VRFY %s ADDR\r\n", user);
    
        if (NMAPSendCommand(nmap, cmd, strlen(cmd)) == (int)strlen(cmd)) {
            while (1) {
                code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE);

                if (code == 1000) {
                    break;
                } else if (code == 2001) {
                    if ((ptr = strchr(buf, ' ')) != '\0') {
                        if (!strcmp(++ptr, BongoBackup.host_addr)) {
                            user_exists = TRUE;
                        } else {
                            if (BongoBackup.verbose) {
                                XplConsolePrintf(_("No such user on this host (%s)\n"), user);
                            }

                            NMAPQuit(nmap);
                            return NULL;
                        }
                    } else {
                        if (BongoBackup.verbose) {
                            XplConsolePrintf(_("Couldn't parse NMAP response (VRFY)\n"));
                        }

                        NMAPQuit(nmap);
                        return NULL;
                    }
                } else {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("Unrecognized NMAP response (VRFY): %s\n"), buf);
                    }

                    NMAPQuit(nmap);
                    return NULL;
                }
            }
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("NMAP connection error (VRFY)\n"));
            }

            NMAPQuit(nmap);
            return NULL;
        }

        if (user_exists) {
            NMAPSendCommand(nmap, "RSET\r\n", 6);
            if (NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE) != 1000) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("NMAP command failed (RSET): %s\n"), buf);
                }
    
                NMAPQuit(nmap);
                return NULL;
            }
    
            sprintf(cmd, "USER %s\r\n", user);
            NMAPSendCommand(nmap, cmd, strlen(cmd));
            if (NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE) != 1000) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("NMAP command failed (USER): %s\n"), buf);
                }
    
                NMAPQuit(nmap);
                return NULL;
            }
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("No such user on this host (%s)\n"), user);
            }

            NMAPQuit(nmap);
            return NULL;
        }
    }

    return nmap;
}

void
ReleaseConnection(Connection *nmap)
{
    NMAPQuit(nmap);
}

int
DocumentBackup(char* user_name, char* document_guid, FILE *data_file, sqlite3 *db, Connection *nmap)
{
    char buf[1024];
    char cmd[1024];
    int code;

    int history_id = 0;
    int last_event = DOCSTATE_UNDEFINED;
    int new_event = 0;

    int document_update = 0;
    int document_length = 0;
    int document_position = 0;

    char *property_name;
    int property_length;
    char *remote_value;
    char *local_value;

    int next_line;
    int num_lines;

    if (SelectHistoricEvent(db, document_guid, BongoBackup.backup_date, &last_event) < 0) {
        return -1;
    }

    /* get all properties for this document */
    sprintf((char *) cmd, "DPGET %s\r\n", document_guid);

    if (NMAPSendCommand(nmap, cmd, strlen(cmd)) != (int)strlen(cmd)) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("NMAP connection error (DPGET)\n"));
        }

        return -1;
    }

    while (1) {
        code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE);

        if (code == 1000) {
            break;
        } else if (code == 2001) {
            /* got property */
            if ((property_name = strchr(buf, ' ')) == '\0') {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Could not parse NMAP response (DPGET)\n"));
                }

                return -1;
            }

            *property_name = '\0';
            property_length = atoi(++property_name);
            property_name = buf;

            remote_value = malloc(property_length + 1);

            if (!remote_value) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Insufficient memory available\n"));
                }

                return -1;
            }

            if (NMAPReadCount(nmap, remote_value, property_length) != property_length) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("NMAP connection error (DPGET)\n"));
                }

                return -1;
            }

            remote_value[property_length] = '\0';
            local_value = NULL;

            if (SelectPropertyValue(db, document_guid, property_name, BongoBackup.backup_date, &local_value) < 0) {
                return -1;
            }

            /* update property if modified */
            if (last_event == DOCSTATE_UNDEFINED || strcmp(local_value, remote_value)) {
                if (!history_id) {
                    new_event = DOCSTATE_MODIFIED;

                    if (last_event == DOCSTATE_UNDEFINED || last_event == DOCSTATE_DELETED) {
                        new_event = DOCSTATE_CREATED;
                    }

                    history_id = InsertHistory(db, document_guid, new_event, BongoBackup.backup_date);

                    if (!history_id) {
                        if (BongoBackup.verbose) {
                            XplConsolePrintf(_("Database query failure (QUERY_INS_HIST)\n"));
                        }

                        if (local_value) {
                            free(local_value);
                        }

                        free(remote_value);
                        return -1;
                    }
                }

                if (!InsertProperty(db, history_id, property_name, remote_value)) {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("Database query failure (QUERY_INS_PROP)\n"));
                    }

                    if (local_value) {
                        free(local_value);
                    }

                    free(remote_value);
                    return -1;
                }

                /* will use later to determine if we need to update document data */
                if (strcmp(property_name, "nmap.modified")) {
                    document_update = 1;
                }
            }

            if (local_value) {
                free(local_value);
            }

            free(remote_value);
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Unrecognized NMAP response (DPGET): %s\n"), buf);
            }

            return -1;
        }
    }

    /* if document has been modified, read and store */
    if (document_update) {
        sprintf(cmd, "DREAD %s\r\n", document_guid);

        if (NMAPSendCommand(nmap, cmd, strlen(cmd)) != (int)strlen(cmd)) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("NMAP connection error (DREAD)\n"));
            }

            return -1;
        }

        while (1) {
            code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE);

            if (code == 1000) {
                break;
            } else if (code == 2002) {
                /* got document */
                document_length = atoi(buf);
                document_position = ftell(data_file);

                if (ConnReadToFile(nmap, data_file, document_length) != document_length) {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("NMAP connection error (DREAD)\n"));
                    }

                    return -1;
                }

                if (!InsertDocument(db, history_id, document_length, document_position)) {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("Database query failure (QUERY_INS_DOC)\n"));
                    }

                    return -1;
                }
            } else if (code == 2003) {
                /* read folder contents (just throw this away) */
                num_lines = atoi(buf);

                for (next_line = 0; next_line < num_lines; next_line++) {
                    ConnReadLine(nmap, buf, sizeof(buf));
                    /* printf((char *) buf2); */
                }
            } else {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Unrecognized NMAP response (DREAD): %s\n"), buf);
                }

                return -1;
            }
        }
    }

    if (BongoBackup.verbose) {
        XplConsolePrintf("%s (%d)\n", document_guid, new_event);
    } else {
        putchar('.');
    }

    fflush(stdout);
    return new_event;
}

int
UserBackup(char* user_name, FILE *data_file, sqlite3 *db, Connection *nmap1, Connection *nmap2)
{
    int event;
    char buf[1024];
    int code;
    char *document_guid;

    char tmp_db_name[256];
    sqlite3 *tmp_db;
    sqlite3_stmt *tmp_stmt_insert;
    sqlite3_stmt *tmp_stmt_select;

    sqlite3_stmt *stmt_all_docs;

    /* get list of user's documents */
    if (NMAPSendCommand(nmap1, "DINFO\r\n", 7) != 7) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("NMAP connection error (DINFO)\n"));
        }

        return 0;
    }

    /* I'm using this temp database to store guids that are observed 
       during backup.  This should help keep the backup database optimal
       and so that it doesn't have to manage any unnecessary data.  */

    sprintf(tmp_db_name, "/tmp/bongobackup-%lu.db", BongoBackup.backup_date);
    sqlite3_open(tmp_db_name, &tmp_db);
    sqlite3_exec(tmp_db, "CREATE TABLE document (document_id INTEGER PRIMARY KEY AUTOINCREMENT, guid VARCHAR(47));", NULL, NULL, NULL);
    sqlite3_prepare(tmp_db, "INSERT INTO document (guid) VALUES (?);", -1, &tmp_stmt_insert, 0);
    sqlite3_prepare(tmp_db, "SELECT document_id FROM document WHERE guid=?;", -1, &tmp_stmt_select, 0);

    while (1) {
        code = NMAPReadAnswer(nmap1, buf, sizeof(buf), TRUE);

        if (code == 1000) {
            break;
        } else if (code == 2001) {
            /* got document */
            if ((document_guid = strchr(buf, ' ')) == '\0') {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Could not parse NMAP response (DINFO)\n"));
                }

                return 0;
            }

            *document_guid = '\0';
            document_guid = buf;

            if ((event = DocumentBackup(user_name, document_guid, data_file, db, nmap2)) > -1) {
                /* write guid to temp file */
                sqlite3_bind_text(tmp_stmt_insert, 1, document_guid, -1, SQLITE_STATIC);
                sqlite3_step(tmp_stmt_insert);
                sqlite3_reset(tmp_stmt_insert);
            } else {
                return 0;
            }
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Unrecognized NMAP response (DINFO): %s\n"), buf);
            }

            return 0;
        }
    }

    /* set all documents not observed during backup to Deleted state (DOCSTATE_DELETED) */
    sqlite3_prepare(db, QUERY_ALL_DOCS, -1, &stmt_all_docs, 0);
    sqlite3_bind_int(stmt_all_docs, 2, BongoBackup.backup_date);

    while (sqlite3_step(stmt_all_docs) == SQLITE_ROW) {
        const char *tmp_guid = sqlite3_column_text(stmt_all_docs, 0);
        sqlite3_bind_text(tmp_stmt_select, 1, tmp_guid, -1, SQLITE_STATIC);

        if (sqlite3_step(tmp_stmt_select) == SQLITE_OK) {
            InsertHistory(db, tmp_guid, DOCSTATE_DELETED, BongoBackup.backup_date);
        }

        sqlite3_reset(stmt_all_docs);
    }

    sqlite3_finalize(stmt_all_docs);

    sqlite3_finalize(tmp_stmt_insert);
    sqlite3_finalize(tmp_stmt_select);
    sqlite3_close(tmp_db);
    remove(tmp_db_name);

    return 1;
}

int
BackupDocument(char* user_name, char* document_guid)
{
    int result = 1;

    FILE *data_file;
    sqlite3 *db;
    Connection *nmap;
    char db_filename[1024];
    char data_filename[1024];

    sprintf(data_filename, "%s/%s%s", BongoBackup.store_path, user_name, DATA_EXT);

    if ((data_file = fopen(data_filename, "a"))) {
        sprintf(db_filename, "%s/%s%s", BongoBackup.store_path, user_name, DB_EXT);

        if (OpenDatabase(db_filename, &db, 1)) {
            if ((nmap = GetConnection(user_name))) {
                if (DocumentBackup(user_name, document_guid, data_file, db, nmap) < 0) {
                    result = 0;
                }
                ReleaseConnection(nmap);
            }
        }

        if (!CloseDatabase(db)) {
            result = 0;
        }

        if (fclose(data_file)) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't close file (%s)\n"), data_filename);
            }

            result = 0;
        }
    } else {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't open file (%s)\n"), data_filename);
        }

        result = 0;
    }

    return result;
}

int
BackupUser(char* user_name)
{
    int result = 1;

    FILE *data_file;
    sqlite3 *db;
    Connection *nmap1;
    Connection *nmap2;
    char db_filename[1024];
    char data_filename[1024];

    /* Try getting a connection first (just in case the user doesn't exist) */
    if ((nmap1 = GetConnection(user_name))) {
        sprintf(data_filename, "%s/%s%s", BongoBackup.store_path, user_name, DATA_EXT);

        if ((data_file = fopen(data_filename, "a"))) {
            sprintf(db_filename, "%s/%s%s", BongoBackup.store_path, user_name, DB_EXT);

            if (OpenDatabase(db_filename, &db, 1)) {
                if ((nmap2 = GetConnection(user_name))) {
                    result = UserBackup(user_name, data_file, db, nmap1, nmap2);
                    ReleaseConnection(nmap2);
                } else {
                    result = 0;
                }
            } else {
                result = 0;
            }

            if (!CloseDatabase(db)) {
                result = 0;
            }

            if (fclose(data_file)) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Couldn't close file (%s)\n"), data_filename);
                }

                result = 0;
            }
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't open file (%s)\n"), data_filename);
            }

            result = 0;
        }

        ReleaseConnection(nmap1);
    } else {
        result = 0;
    }

    return result;
}

int
DocumentRestore(char* user_name, char* document_guid, FILE *data_file, sqlite3 *db, Connection *nmap)
{
    char cmd[256];
    char buf[256];
    int qc;
    int code;

    char document_resource[256];
    int document_type;
    int document_length;
    int document_position;

    sqlite3_stmt *stmt;

    document_resource[0] = '\0';
    document_type = 0;
    document_length = 0;
    qc = SelectDocumentInfo(db, document_guid, BongoBackup.backup_date, document_resource, &document_type, &document_length);

    if (qc > 0) {
        document_position = 0;
        qc = SelectDocumentAttributes(db, document_guid, BongoBackup.backup_date, NULL, &document_position);

        if (qc > 0) {
            if (!NMAP_IS_FOLDER(document_type)) {
                /* resoure is non-folder */
                sprintf(cmd, "DSTOR %s %d %d G%s\r\n", document_resource, document_type, document_length, document_guid);
    
                if (NMAPSendCommand(nmap, cmd, strlen(cmd)) == (int)strlen(cmd)) {
                    while ((code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE)) != 1000) {
                        if (code == 2002) {
                            if (!fseek(data_file, document_position, SEEK_SET)) {
                                if (ConnWriteFromFile(nmap, data_file, document_length) != document_length) {
                                    if (BongoBackup.verbose) {
                                        XplConsolePrintf(_("NMAP connection error (DSTOR)\n"));
                                    }

                                    return -1;
                                }
                            } else {
                                if (BongoBackup.verbose) {
                                    XplConsolePrintf(_("Couldn't set file position\n"));
                                }

                                return -1;
                            }
                        } else {
                            if (BongoBackup.verbose) {
                                XplConsolePrintf(_("Unrecognized NMAP response (DSTOR): %s\n"), buf);
                            }

                            return -1;
                        }
                    }
                } else {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("NMAP connection error (DSTOR)\n"));
                    }

                    return -1;
                }
            } else {
                /* resource is a folder */
                sprintf(cmd, "DCREA %s %d %s\r\n", document_resource, document_type, document_guid);

                if (NMAPSendCommand(nmap, cmd, strlen(cmd)) == (int)strlen(cmd)) {
                    if ((code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE)) != 1000) {
                        if (BongoBackup.verbose) {
                            XplConsolePrintf(_("Unrecognized NMAP response (DCREA): %s\n"), buf);
                        }

                        return -1;
                    }
                } else {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("NMAP connection error (DCREA)\n"));
                    }

                    return -1;
                }
            }

            /* DPSET all properties not sent in DSTOR command */
            sqlite3_prepare(db, QUERY_ALL_PROPS, -1, &stmt, 0);
            
            sqlite3_bind_text(stmt, 1, document_guid, -1, SQLITE_STATIC);
            sqlite3_bind_int(stmt, 2, BongoBackup.backup_date);
            sqlite3_bind_text(stmt, 3, document_guid, -1, SQLITE_STATIC);

            while ((qc = sqlite3_step(stmt)) == SQLITE_ROW) {
                const char *prop_name = sqlite3_column_text(stmt, 1);
                const char *prop_val = sqlite3_column_text(stmt, 2);
                int prop_length = strlen(prop_val);

                if (strcmp(prop_name, "nmap.resource") && strcmp(prop_name, "nmap.type") && 
                    strcmp(prop_name, "nmap.length") && strcmp(prop_name, "nmap.guid")) {

                    sprintf(cmd, "DPSET %s %d\r\n", prop_name, prop_length);
    
                    if (NMAPSendCommand(nmap, cmd, strlen(cmd)) == (int)strlen(cmd)) {
                        while ((code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE)) != 1000) {
                            if (code == 2002) {
                                if (ConnWrite(nmap, prop_val, prop_length) != prop_length) {
                                    if (BongoBackup.verbose) {
                                        XplConsolePrintf(_("NMAP connection error (DPSET)\n"));
                                    }

                                    sqlite3_finalize(stmt);
                                    return -1;
                                }
                            } else {
                                if (BongoBackup.verbose) {
                                    XplConsolePrintf(_("Unrecognized NMAP response (DPSET): %s\n"), buf);
                                }

                                sqlite3_finalize(stmt);
                                return -1;
                            }
                        }
                    } else {
                        if (BongoBackup.verbose) {
                            XplConsolePrintf(_("NMAP connection error (DPSET)\n"));
                        }

                        sqlite3_finalize(stmt);
                        return -1;
                    }
                }
            }

            if (qc != SQLITE_DONE) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Query failed to execute (QUERY_ALL_PROPS)\n"));
                }

                return -1;
            }

            if (sqlite3_finalize(stmt) != SQLITE_OK) {
                if (BongoBackup.verbose) {
                    XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_ALL_PROPS)\n"));
                }

                return -1;
            }
        } else if (qc == 0) {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Document not found in store\n"));
            }

            return 0;
        } else {
            return -1;
        }
    } else if (qc == 0) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Document not found in store\n"));
        }

        return 0;
    } else {
        return -1;
    }

    if (BongoBackup.verbose) {
        XplConsolePrintf("%s\n", document_guid);
    } else {
        putchar('.');
    }

    fflush(stdout);
    return 1;
}

int
UserRestore(char* user_name, FILE *data_file, sqlite3 *db, Connection *nmap)
{
    int qc;
    sqlite3_stmt *stmt;
    char document_guid[47];

    char buf[256];
    char cmd[256];
    int code;

    char *property_name;
    int property_length = 0;
    char property_value[256];
    int remote_modified;
    int local_modified;
    int restore = 0;

    if (sqlite3_prepare(db, QUERY_DOCS_MOD, -1, &stmt, 0) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't create prepared statement (QUERY_DOCS_MOD)\n"));
        }

        return 0;
    }
    
    if (sqlite3_bind_int(stmt, 1, BongoBackup.backup_date) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't bind to prepared statement (QUERY_DOCS_MOD)\n"));
        }

        sqlite3_finalize(stmt);
        return 0;
    }

    while ((qc = sqlite3_step(stmt)) == SQLITE_ROW) {
        strcpy(document_guid, sqlite3_column_text(stmt, 0));
        local_modified = sqlite3_column_int(stmt, 1);
        restore = 0;

        /* DPGET for named properties not currently implemented in NMAP */
        sprintf(cmd, "DPGET %s nmap.modified\r\n", document_guid);

        if (NMAPSendCommand(nmap, cmd, strlen(cmd)) == (int)strlen(cmd)) {
            if ((code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE)) == 2001) {
                if ((property_name = strchr(buf, ' ')) == '\0') {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("Couldn't parse NMAP response (DPGET)\n"));
                    }

                    return 0;
                }
    
                *property_name = '\0';
                property_length = atoi(++property_name);
                property_name = buf;
    
                if (NMAPReadCount(nmap, property_value, property_length) != property_length) {
                    if (BongoBackup.verbose) {
                        XplConsolePrintf(_("NMAP connection error (DPGET)\n"));
                    }

                    return 0;
                }
    
                property_value[property_length] = '\0';
                remote_modified = atoi(property_value);

                if (local_modified > remote_modified) {
                    restore = 1;
                }
            } else if (code == 4220) {
                restore = 1;
            }
        }

        if (restore && DocumentRestore(user_name, document_guid, data_file, db, nmap) < 0) {
            sqlite3_finalize(stmt);
            return 0;
        }
    }
    
    if (qc != SQLITE_DONE) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Database query failure (QUERY_DOCS_MOD)\n"));
        }

        sqlite3_finalize(stmt);
        return 0;
    }

    if (sqlite3_finalize(stmt) != SQLITE_OK) {
        if (BongoBackup.verbose) {
            XplConsolePrintf(_("Couldn't delete prepared statement (QUERY_DOCS_MOD)\n"));
        }

        return 0;
    }

    return 1;
}

int
RestoreDocument(char* user_name, char* document_guid)
{
    int result = 1;
    Connection *nmap;

    sqlite3 *db;
    char db_filename[1024];
    char data_filename[1024];
    FILE *data_file;

    sprintf(db_filename, "%s/%s%s", BongoBackup.store_path, user_name, DB_EXT);

    if (OpenDatabase(db_filename, &db, 0)) {
        sprintf(data_filename, "%s/%s%s", BongoBackup.store_path, user_name, DATA_EXT);
        
        if ((data_file = fopen(data_filename, "r"))) {
            if ((nmap = GetConnection(user_name))) {
                if (DocumentRestore(user_name, document_guid, data_file, db, nmap) < 0) {
                    result = 0;
                }

                ReleaseConnection(nmap);
            } else {
                result = 0;
            }
    
            fclose(data_file);
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't open file (%s)\n"), data_filename);
            }

            result = 0;
        }
    } else {
        result = 0;
    }

    if (!CloseDatabase(db)) {
        result = 0;
    }

    return result;
}

int
RestoreUser(char* user_name)
{
    int result = 1;
    Connection *nmap;

    sqlite3 *db;
    char db_filename[1024];
    char data_filename[1024];
    FILE *data_file;

    sprintf(db_filename, "%s/%s%s", BongoBackup.store_path, user_name, DB_EXT);

    if (OpenDatabase(db_filename, &db, 0)) {
        sprintf(data_filename, "%s/%s%s", BongoBackup.store_path, user_name, DATA_EXT);
        
        if ((data_file = fopen(data_filename, "r"))) {
            if ((nmap = GetConnection(user_name))) {
                result = UserRestore(user_name, data_file, db, nmap);
                ReleaseConnection(nmap);
            } else {
                result = 0;
            }
    
            fclose(data_file);
        } else {
            if (BongoBackup.verbose) {
                XplConsolePrintf(_("Couldn't open file (%s)\n"), data_filename);
            }

            result = 0;
        }
    } else {
        result = 0;
    }

    if (!CloseDatabase(db)) {
        result = 0;
    }

    return result;
}

/*  SearchStore
 *
 *  Prints GUIDs for documents that match search criteria.  Note: All args are optional.
 *
 *  In:
 *       char **strings:       strings to search for
 *       char **users:         user names to include
 *       char **types:         document types to include (mail, calendar, addressbook)
 *.......unsigned long before: include documents before this date
 *       unsigned long after:  include documents after this date
 *       char **guids:         include these documents
 *
 *  Return:
 *       non-zero for success, zero for failure
 */
int
SearchStore(char **strings, char **users, char **types, unsigned long before, unsigned long after, char **guids)
{
    if (!users) {
        /* full user search */
    } else {
        /* partial user search */
        if (!types) {
            /* full type search */
        } else {
            /* partial type search */
        }
    }

    return 0;
}

/*  RollbackStore
 *
 *  Truncate store by removing all data stored after [date].
 *
 *  In:
 *       unsigned long date: date to truncate by
 *
 *  Return:
 *       non-zero for success, zero for failure
 */
int
RollbackStore(unsigned long date)
{
    return 0;
}

int 
main(int argc, char *argv[])
{
    int i;
    int j;
    Connection *nmap;
    char buf[1024];
    int code;

    int no_err = 1;
    int command = 0;

    int next_arg = 0;
    char *arg_ptr = NULL;
    struct stat stat_buf;

    int next_backup_arg = 0;
    BackupArg backup_args[256];

    DIR *store_dir;
    char *d_ptr;
    struct dirent *d_ent;

    XplInit();
	
	char *usage = 
        _("bongobackup - Backup & restore a Bongo NMAP document store.\n\n"
        "Usage: bongobackup [options] [command]\n\n"
        "Options:\n"
        "  -h, --host=[ip address]         defaults to '127.0.0.1'\n"
        "  -p, --passwd=[password]         password used to authenticate\n"
        "  -s, --store=[path]              path to local backup store\n"
        "  -u, --user=[user]               username used to authenticate\n"
        "  -v, --verbose                   verbose output\n\n"
        "Commands:\n"
        "  backup [user[:guid[,..]][ ..]]  appends incremental backup to the store\n"
        "  restore [user[:guid[,..]][ ..]] restores documents from store to server\n"
        "  search [filter[,..]]            searches the store for documents\n"
        "  rollback [MM/DD/YYYY]           permanently deletes data store after date\n\n"
        "Search Filters:\n"
        "  after=[MM/DD/YYYY]              only search documents after this date\n"
        "  before=[MM/DD/YYYY]             only search documents before this date\n"
        "  string=[string[:..]]            strings may be enclosed in single quotes ('')\n"
        "  type=[type[:..]]                only search type(s) (mail, cal, ab)\n"
        "  user=[user[:..]]                only search documents owned by user(s)\n");

    XplConsolePrintf(_("Bongo Backup: "));

    /* defaults */
    BongoBackup.host_addr = "127.0.0.1";
    BongoBackup.store_path = ".";
    BongoBackup.auth_password = "bongo";
    BongoBackup.auth_username = "admin";
    BongoBackup.backup_date = time(NULL);
    BongoBackup.verbose = 0;

    /* parse options */
    while (++next_arg < argc && argv[next_arg][0] == '-') {
        if ((arg_ptr = strchr(argv[next_arg], '=')) != '\0') {
            *arg_ptr = '\0';

            if (!strcmp(argv[next_arg], "-h") || !strcmp(argv[next_arg], "--host")) {
                BongoBackup.host_addr = arg_ptr + 1;
            } else if (!strcmp(argv[next_arg], "-p") || !strcmp(argv[next_arg], "--passwd")) {
                BongoBackup.auth_password = arg_ptr + 1;
            } else if (!strcmp(argv[next_arg], "-s") || !strcmp(argv[next_arg], "--store")) {
                BongoBackup.store_path = arg_ptr + 1;
    
                /* make sure path is valid */
                if (stat(BongoBackup.store_path, &stat_buf) == -1 || !S_ISDIR(stat_buf.st_mode)) {
                    XplConsolePrintf(_("Invalid store path: %s\n"), BongoBackup.store_path);
                    next_arg = argc;
                }
            } else if (!strcmp(argv[next_arg], "-u") || !strcmp(argv[next_arg], "--user")) {
                BongoBackup.auth_username = arg_ptr + 1;
            } else {
                XplConsolePrintf(_("Unrecognized option: %s\n"), argv[next_arg]);
            }
        } else if (!strcmp(argv[next_arg], "-v") || !strcmp(argv[next_arg], "--verbose")) {
            BongoBackup.verbose = 1;
        } else {
            XplConsolePrintf(_("Unrecognized option: %s\n"), argv[next_arg]);
        }
    }

    /* parse command */
    if (next_arg < argc) {
        if (!strcmp(argv[next_arg], "backup")) { 
            command = 1;
        } else if (!strcmp(argv[next_arg], "restore")) {
            command = 2;
        } else if (!strcmp(argv[next_arg], "search")) { 
            next_arg = argc;
            command = 3;
        } else {
            XplConsolePrintf(_("Unrecognized command: %s\n"), argv[next_arg]);
        }
    } else {
        XplConsolePrintf(_("No command specified\n"));
    }

    /* parse additional command args */
    switch (command) {
        case 1:
        case 2:
            while (++next_arg < argc && next_backup_arg < 256) {
                backup_args[next_backup_arg].next_guid = 0;
                backup_args[next_backup_arg].user = argv[next_arg];

                if ((arg_ptr = strchr(argv[next_arg], ':')) != '\0') {
                    *arg_ptr = '\0';
                    arg_ptr++;
                    backup_args[next_backup_arg].guids[backup_args[next_backup_arg].next_guid] = arg_ptr;
                    backup_args[next_backup_arg].next_guid++;

                    while ((arg_ptr = strchr(arg_ptr, ',')) != '\0') {
                        *arg_ptr = '\0';
                        arg_ptr++;
                        backup_args[next_backup_arg].guids[backup_args[next_backup_arg].next_guid] = arg_ptr;
                        backup_args[next_backup_arg].next_guid++;
                    }
                }

                next_backup_arg++;
            }
            break;
        case 3:
        default:
            break;
    }

    if (command == 1) {
        /* backup */
        XplConsolePrintf(_("Backing up data\n"));

        if (ConnStartup((300))) {
            if ((nmap = GetConnection(NULL))) {
                if (next_backup_arg < 1) {
                    /* full backup */
                    NMAPSendCommand(nmap, "ULIST\r\n", 7);
    
                    while (no_err) {
                        code = NMAPReadAnswer(nmap, buf, sizeof(buf), TRUE);
    
                        if (code == 1000) {
                            break;
                        } else if (code == 2001) {
                            /* got username */    
                            no_err = BackupUser(buf);
                        } else {
                            XplConsolePrintf(_("Unrecognized NMAP response (ULIST): %s\n"), buf);
                            no_err = 0;
                        }
                    }
                } else {
                    /* partial backup */
                    for (i = 0; no_err && i < next_backup_arg; i++) {
                        if (backup_args[i].next_guid < 1) {
                            no_err = BackupUser(backup_args[i].user);
                        } else {
                            for (j = 0; no_err && j < backup_args[i].next_guid; j++) {
                                no_err = BackupDocument(backup_args[i].user, backup_args[i].guids[j]);
                            }
                        }
                    }
                }

                ReleaseConnection(nmap);
            } else {
                no_err = 0;
            }

            if (!BongoBackup.verbose) {
                XplConsolePrintf("\n");
            }

            if (no_err) {
                XplConsolePrintf(_("Backup complete\n"));
            } else {
                XplConsolePrintf(_("Backup failed\n"));
            }

            ConnShutdown();
        } else {
            XplConsolePrintf(_("Failed to initialize connection manager\n"));
        }
    } else if (command == 2) {
        /* restore */
        XplConsolePrintf(_("Restoring data\n"));

        if (ConnStartup((300))) {
            if (next_backup_arg < 1) {
                /* full restore */
                if ((store_dir = opendir(BongoBackup.store_path))) {
                    while (no_err && (d_ent = readdir(store_dir))) {
                        if ((d_ptr = strchr(d_ent->d_name, '.')) != '\0') {
                            *d_ptr++ = '\0';

                            if (!strcmp(d_ptr, "data")) {
                                no_err = RestoreUser(d_ent->d_name);
                            }
                        }
                    }
                } else {
                    XplConsolePrintf(_("Couldn't open store directory %s\n"), BongoBackup.store_path);
                }
            } else {
                /* partial restore */
                for (i = 0; no_err && i < next_backup_arg; i++) {
                    if (backup_args[i].next_guid < 1) {
                        no_err = RestoreUser(backup_args[i].user);
                    } else {
                        for (j = 0; no_err && j < backup_args[i].next_guid; j++) {
                            no_err = RestoreDocument(backup_args[i].user, backup_args[i].guids[j]);
                        }
                    }
                }
            }

            if (!BongoBackup.verbose) {
                XplConsolePrintf("\n");
            }

            if (no_err) {
                XplConsolePrintf(_("Restore complete\n"));
            } else {
                XplConsolePrintf(_("Restore failed\n"));
            }

            ConnShutdown();
        } else {
            XplConsolePrintf(_("Failed to initialize connection manager\n"));
        }
    } else if (command == 3) {
        /* search */
        XplConsolePrintf(_("Search command not implemented\n"));
    } else {
        /* help */
        XplConsolePrintf("%s", usage);
    }

    return 1;
}
