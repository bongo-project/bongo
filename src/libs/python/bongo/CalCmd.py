from bongo.libs import calcmd
from datetime import datetime

class Command:
    def __init__(self, cmdstr, tzid):
        self._cmd = calcmd.New(cmdstr, tzid)
        self._begin = None
        self._data = None
        self._end = None
        self._type = None
        self._json = None
        self._conciseRange = None

    def GetBegin(self):
        if self._begin is None:
            time = calcmd.GetBegin(self._cmd)
            self._begin = self._DateTimeFromTime(time)
        return self._begin

    def GetData(self):
        if self._data is None:
            self._data = calcmd.GetData(self._cmd)
        return self._data

    def GetEnd(self):
        if self._end is None:
            time = calcmd.GetEnd(self._cmd)
            self._end = self._DateTimeFromTime(time)
        return self._end

    def GetType(self):
        if self._type is None:
            self._type = calcmd.GetType(self._cmd)
        return self._type

    def _DateTimeFromTime(self, time):
        return datetime(time[5], time[4], time[3],
                        time[2], time[1], time[0])

    def GetJson(self) :
        if self._json is None :
            self._json = calcmd.GetJson(self._cmd)
        return self._json
            
    def GetConciseDateTimeRange(self) :
        if self._conciseRange is None:
            self._conciseRange = calcmd.GetConciseDateTimeRange(self._cmd)
        return self._conciseRange
    
