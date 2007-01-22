#ifndef ALARMDB_H
#define ALARMDB_H

#include <sqlite3.h>

typedef struct _AlarmDBHandle AlarmDBHandle;

typedef struct {
    char *store;
    uint64_t guid;
    uint64_t offset;
    BongoCalTime time;  /* UTC */
    const char *summary;
    const char *email;
    const char *sms;
} AlarmInfo;

typedef void * AlarmStmt;
typedef void * AlarmInfoStmt;


AlarmDBHandle * AlarmDBOpen(BongoMemStack *memstack);

void AlarmDBClose(AlarmDBHandle *handle);

AlarmInfoStmt * AlarmDBListAlarms(AlarmDBHandle *handle, 
                                  uint64_t start, uint64_t end);

int AlarmStmtStep(AlarmDBHandle *handle, AlarmStmt *stmt);

void AlarmStmtEnd(AlarmDBHandle *handle, AlarmStmt *stmt);

int AlarmInfoStmtStep(AlarmDBHandle *hanldle, AlarmInfoStmt *stmt, AlarmInfo *info);

int AlarmDBSetAlarm(AlarmDBHandle *handle, AlarmInfo *info);

int AlarmDBDeleteAlarms(AlarmDBHandle *handle, const char *store, uint64_t guid);


#endif
