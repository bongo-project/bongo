JCal = Dragonfly.JCal;

value = function (day, time, tzid) {
    var JCal = Dragonfly.JCal;
    if (!time) {
        return { "value": day, "params": { "type": "DATE" } };
    } else if (!tzid) {
        return { "value": day + 'T' + time };
    } else if (tzid == '/bongo/UTC') {
        return { "value": day + 'T' + time + 'Z'};
    }    
    return { "value": day + 'T' + time, "params": { "tzid": tzid} };
}

// Builds a jcal occurrence for the first instance of a jcal
makeOccurrence = function (jcalevent, dtstart, dtend) {
    dtstart = (dtstart) ? value(dtstart) : jcalevent.dtstart;
    var occurrence = {
        "dtstart": deepmerge (dtstart, {valueLocal: $V(dtstart)}),
        "uid": deepclone (jcalevent.uid),
        "recurid": deepclone (dtstart),
        "generated": true
    };
    if (jcalevent.duration) {
        occurrence.dtend = JCal.durationToDtend (jcalevent.dtstart, jcalevent.duration);
    } else {
        dtend = (dtend) ? value(dtend) : jcalevent.dtend;
        occurrence.dtend = update (deepclone (dtend), { valueLocal: $V(dtend) });
    }
    return occurrence;
}

deepmerge = function (obj1, obj2) 
{
    return update (deepclone(obj1), obj2);
}

///////////////////////////// Test Events /////////////////////////////////
// Type of datetimes: Un = untimed, Fl = floating, Ut = UTC, Tz = timezone
// Recurrences: R[d,w,m,y][f,u,c] = repeat [daily|weekly|monthly|yearly][forever|until|count]

initTzid = "/bongo/America/New_York";
finalTzid = "/bongo/Europe/Paris";
initDay  = "20060705";
finalDay = "20060704";
initStartTime = initDay + "T120000";
initEndTime = "20060705T130000";
finalStartTime = "20060705T140000";
finalEndTime = "20060705T160000";

initStartTimeO1 = "20060724T120000";
initEndTimeO1 = "20060724T130000";
finalStartTimeO1 = "20060724T140000";
finalEndTimeO1 = "20060724T160000";

initDuration = {"value":"PT1H"};
initUnDuration = {"value":"P1D"};

initUnStart = value("20060705");
initUnEnd = value("20060706");
finalUnStart = value("20070705");
finalUnEnd = value("20070706");

initFlStart = { "value": initStartTime };
initFlEnd = { "value": initEndTime };
finalFlStart = { "value": finalStartTime };
finalFlEnd = { "value":finalEndTime };

initUtStart = {"value": initStartTime + "Z"};
initUtEnd = {"value": initEndTime + "Z"};
finalUtStart = {"value": finalStartTime + "Z"};
finalUtEnd = {"value": finalEndTime + "Z"};

initTzStart = { "value": initStartTime, "params": { "tzid": initTzid} };
initTzEnd = { "value": initEndTime, "params": {"tzid": initTzid} };
finalTzStart = { "value": finalStartTime, "params": { "tzid": initTzid} };
finalTzEnd = { "value": finalEndTime, "params": { "tzid": initTzid} };

initTzStartO1 = {"value":initStartTimeO1, "params":{ "tzid": initTzid} };
initTzEndO1 = {"value":initEndTimeO1, "params": { "tzid": initTzid} };
finalTzStartO1 = {"value":finalStartTimeO1, "params":{ "tzid": initTzid} };
finalTzEndO1 = {"value":finalEndTimeO1, "params": { "tzid": initTzid} };

baseException = {
    "type":"event",
    "uid":{"value":"20060705T184500.740-475312357@vader"}
};

baseEvent = deepmerge (baseException, { "summary": { "value": "New Event" } });

initTzEvent = deepmerge (baseEvent, {
    "dtstart": deepclone (initTzStart), 
    "dtend": deepclone (initTzEnd) 
});

finalTzEvent = deepmerge (baseEvent, {
    "dtstart": deepclone (finalTzStart), 
    "dtend": deepclone (finalTzEnd) 
});

initTzDuEvent = deepmerge (baseEvent, {
    "dtstart": deepclone (initTzStart), 
    "duration": deepclone (initDuration)
});

initFlEvent = deepmerge (baseEvent, {
    "dtstart": deepclone (initFlStart),
    "dtend": deepclone (initFlEnd)
});

initFlDuEvent = deepmerge (baseEvent, {
    "dtstart": deepclone (initFlStart), 
    "duration": deepclone (initDuration) 
});

initUnEvent = deepmerge (baseEvent, { 
    "dtstart": deepclone (initUnStart), 
    "dtend": deepclone (initUnEnd)
});

initUnDuEvent = deepmerge (baseEvent, { 
    "dtstart": deepclone (initUnStart), 
    "duration": initUnDuration
});

initUtEvent = deepmerge (baseEvent, { 
    "dtstart": deepclone (initUtStart),
    "dtend": deepclone (initUtEnd)
});

// Recurrences
RdfRules = [{"freq":"daily", "interval":1 }];
RwfRules = [{"freq":"weekly", "interval":1, "byday":["MO","WE","FR"] }];
initTzRdfEvent = deepmerge (initTzEvent, { "rrules": deepclone (RdfRules) } );
finalTzRdfEvent = deepmerge (finalTzEvent, { "rrules": deepclone (RdfRules) } );
initUnRdfEvent = deepmerge (initUnEvent, { "rrules": deepclone (RdfRules) } );
initTzRwfEvent = deepmerge (initTzEvent, { "rrules": deepclone (RwfRules) } );

initTzOccurrence1 = update (
    makeOccurrence (initTzRdfEvent),
    { "dtstart": deepclone (initTzStartO1), "dtend": deepclone (initTzEndO1) }
);

////////////////////////// Edit Test Cases /////////////////////////////////
var EditTestCases = [
{
    name: 'No-op',
    changes: { },
    start:  deepclone (initTzEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'New Summary',
    changes: {summary:"New Summary"},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, { "summary": { "value": "New Summary" } })
},

{
    name: 'Add Location',
    changes: {location:"New Location"},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, { "location": { "value": "New Location" } })
},

{
    name: 'Change Day',
    changes: {startDay:Day.get (2006, 6, 4), endDay: Day.get (2006, 6, 4)},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, {
        "dtstart": { "value":"20060704T120000", "params":{ "tzid": initTzid } },
        "dtend": { "value": "20060704T130000", "params": {"tzid":initTzid} }
    })
},

{
    name: 'Change day with duration',
    changes: {startDay: JCal.parseDay(finalDay), endDay:  JCal.parseDay(finalDay)},
    start: deepclone (initTzDuEvent),
    finish: deepmerge (baseEvent, {
        "dtstart": { "value":"20060704T120000", "params":{"tzid":initTzid} },
        "dtend": { "value":"20060704T130000", "params":{"tzid":initTzid} }
    })
},

{
    name: 'Change Start Time',
    changes: {startTime: JCal.parseDateTime('20060705T122222')},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, { "dtstart": {"value":"20060705T122222", "params":{"tzid":initTzid} }})
},

{
    name: 'Change End Time',
    changes: {endTime: JCal.parseDateTime('20060705T150000')},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, {
        "dtend": { "value": "20060705T150000", "params":{"tzid": initTzid } }
    })
},

{
    name: 'change end time with duration',
    changes: {endTime: JCal.parseDateTime('20060705T150000')},
    start: deepclone (initTzDuEvent),
    finish: deepmerge (initTzEvent, {
        "dtend": { "value":"20060705T150000", "params":{"tzid":initTzid} }
    })
},


{
    name: 'make untimed and add a day',
    changes: {isUntimed:true, endDay: Day.get (2006, 6, 6)},
    start: deepclone (initTzEvent),
    finish: {
        "type":initTzEvent.type,
        "summary":initTzEvent.summary,
        "uid":initTzEvent.uid,
        "dtstart": {"value": "20060705", "params": { "type": "DATE" } },
        "dtend": {"value": "20060707", "params": { "type": "DATE" } } 
    }
},
{
    name: 'Change Timezone',
    changes: {startTzid: finalTzid, endTzid: finalTzid},
    start: deepclone (initTzEvent),
    finish: deepmerge (initTzEvent, {
        "dtstart": { "value": initTzStart.value, "params":{"tzid":finalTzid} },
        "dtend": { "value": initTzEnd.value, "params":{"tzid":finalTzid} }
    })
},

{
    name: 'change timezone with duration',
    changes: {startTzid: finalTzid, endTzid: finalTzid},
    start: deepclone (initTzDuEvent),
    finish: deepmerge (initTzEvent, {
        "dtstart": { "value": initTzStart.value, "params":{"tzid":finalTzid} },
        "dtend": { "value": initTzEnd.value, "params":{"tzid":finalTzid} }
    })
},

{
    name: 'floating -> timezone',
    changes: {startTzid: '/bongo/America/New_York', endTzid: '/bongo/America/New_York'},
    start: deepclone (initFlEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'floating -> timezone with duration',
    changes: {startTzid: '/bongo/America/New_York', endTzid: '/bongo/America/New_York'},
    start: deepclone (initFlDuEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'timezone -> floating',
    changes: {startTzid: '/bongo/floating', endTzid: '/bongo/floating'},
    start: deepclone (initTzEvent),
    finish: deepclone (initFlEvent)
},

{
    name: 'timezone -> floating with duration',
    changes: {startTzid: '/bongo/floating', endTzid: '/bongo/floating'},
    start: deepclone (initTzDuEvent),
    finish: deepclone (initFlEvent)
},


{
    name: 'timezone -> untimed',
    changes: {isUntimed: true},
    start: deepclone (initTzEvent),
    finish: deepclone (initUnEvent)
},

{
    name: 'timezone -> untimed with duration',
    changes: {isUntimed: true},
    start: deepclone (initTzDuEvent),
    finish: deepclone (initUnEvent)
},

{
    name: 'untimed -> timezone',
    changes: {
        isUntimed: false, startTzid: initTzid, endTzid: initTzid,
        startTime: JCal.parseDateTime(initStartTime),
        endTime: JCal.parseDateTime(initEndTime)
    },
    start: deepclone (initUnEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'untimed -> timezone with duration',
    changes: {
        isUntimed: false, startTzid: initTzid, endTzid: initTzid,
        startTime: JCal.parseDateTime(initStartTime),
        endTime: JCal.parseDateTime(initEndTime)
    },
    start: deepclone (initUnDuEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'floating -> untimed',
    changes: { isUntimed: true },
    start: deepclone (initFlEvent),
    finish: deepclone (initUnEvent)
},

{
    name: 'floating -> untimed with duration',
    changes: { isUntimed: true },
    start: deepclone (initFlDuEvent),
    finish: deepclone (initUnEvent)
},

{
    name: 'untimed -> floating',
    changes: {
        isUntimed: false, startTzid: '/bongo/floating', endTzid: '/bongo/floating',
        startTime: JCal.parseDateTime(initStartTime),
        endTime: JCal.parseDateTime(initEndTime)
    },
    start: deepclone (initUnEvent),
    finish: deepclone (initFlEvent)
},

{
    name: 'untimed -> floating with duration',
    changes: {
        isUntimed: false, startTzid: '/bongo/floating', endTzid: '/bongo/floating',
        startTime: JCal.parseDateTime(initStartTime),
        endTime: JCal.parseDateTime(initEndTime)
    },
    start: deepclone (initUnDuEvent),
    finish: deepclone (initFlEvent)
},

{
    name: 'UTC -> timezone',
    changes: { startTzid: initTzid, endTzid: initTzid },
    start: deepclone (initUtEvent),
    finish: deepclone (initTzEvent)
},

{
    name: 'Add Recurrence Rule',
    changes: { rrules: deepclone (RdfRules) },
    scope: JCal.allScope,
    start: deepclone (initTzEvent),
    finish: deepclone (initTzRdfEvent)
},

{
    name: 'Recurrences: edit summary for all',
    changes: { summary:"New Summary" },
    scope: JCal.allScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, { "summary": { "value": "New Summary" } })
},

{
    name: 'Recurrences: make exception with new summary',
    changes: { summary:"New Summary" },
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.instanceScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, {"exdates": [initTzStartO1] }),
    finishExceptions: {
        '20060724T120000': deepmerge (baseException, { 
            "recurid": deepclone (initTzStartO1),
            "summary": { "value" : "New Summary" },
            "dtstart": deepclone (initTzStartO1), 
            "dtend": deepclone (initTzEndO1)  
        })
    }
},

{
    name: 'Recurrences: make exception with added location',
    changes: { location: "New Location" },
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.instanceScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, {"exdates": [initTzStartO1] }),
    finishExceptions: {
        '20060724T120000': deepmerge (baseException, { 
            "recurid": deepclone (initTzStartO1),
            "location": { "value": "New Location" },
            "dtstart": deepclone (initTzStartO1), 
            "dtend": deepclone (initTzEndO1)  
        })
    }
},

{
    name: 'Recurrences: make exception with new day',
    changes: {startDay:Day.get (2006, 6, 4), endDay: Day.get (2006, 6, 4)},
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.instanceScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, {"exdates": [initTzStartO1] }),
    finishExceptions: {
        '20060724T120000': deepmerge (baseException, {
            "recurid": deepclone (initTzStartO1),
            "dtstart": { "value":"20060704T120000", "params":{ "tzid": initTzid } },
            "dtend": { "value": "20060704T130000", "params": {"tzid":initTzid} }
         })
    }
},

{
    name: 'Recurrences: preserve exception when changing time',
    changes: { startTime: JCal.parseDateTime(finalStartTime), endTime: JCal.parseDateTime(finalEndTime) },
    scope: JCal.allScope,
    start: deepmerge (initTzRdfEvent, {"exdates": [initTzStartO1] }),
    finish: deepmerge (finalTzRdfEvent, {"exdates": [finalTzStartO1] })
},

{
    name: 'Recurrences: change day for all in daily event',
    changes: { startDay: JCal.parseDay ("20060726"), endDay: JCal.parseDay ("20060726") },
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.allScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, {
        "dtstart": { "value":"20060707T120000", "params":{ "tzid": initTzid } },
        "dtend": { "value": "20060707T130000", "params": {"tzid":initTzid } }
    })
},

{
    name: 'Recurrences: preserve exception when changing day and time',
    changes: { 
        startDay: JCal.parseDay ("20060722"), 
        endDay: JCal.parseDay ("20060722"), 
        startTime: JCal.parseDateTime(finalStartTime), 
        endTime: JCal.parseDateTime(finalEndTime) },
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.allScope,
    start: deepmerge (initTzRdfEvent, {
        "exdates": [{ "value":"20060710T120000", "params":{ "tzid": initTzid } }] 
    }),
    finish: deepmerge (initTzRdfEvent, {
        "dtstart": { "value":"20060703T140000", "params":{ "tzid": initTzid } },
        "dtend": { "value": "20060703T160000", "params": {"tzid":initTzid } },
        "exdates": [{ "value":"20060710T140000", "params":{ "tzid": initTzid } }] 
    })
},

{
    name: 'Recurrences: extend untimed event by a day for all',
    changes: { endDay: JCal.parseDay ("20060725") },
    occurrence: makeOccurrence (initUnRdfEvent, "20060724", "20060725"),
    scope: JCal.allScope,
    start: deepclone (initUnRdfEvent),
    finish: deepmerge (initUnRdfEvent, { "dtend": value("20060707") })
},

{
    name: 'Recurrences: change day for all in weekly event',
    changes: { startDay: JCal.parseDay ("20060725"), endDay: JCal.parseDay ("20060725") },
    occurrence: deepclone (initTzOccurrence1),
    scope: JCal.allScope,
    start: deepclone (initTzRdfEvent),
    finish: deepmerge (initTzRdfEvent, {
        "rrules": [{"freq":"weekly", "interval":1, "byday":["TU","WE","FR"] }]
    })
}

];  // end EditTests

///////////////////////////// Test Case Runner /////////////////////////////////

runEditTest = function (test)
{
    var start = (test.start instanceof Array) ? test.start : [test.start];
    try {
        var jsob = {
            jcal: { version: { value: '2.0' }, components: start },
            occurrences: [test.occurrence || makeOccurrence (test.start)]
        };
        var vcal = new JCal.VCalendar (jsob);
        vcal.update (vcal.occurrences[0], test.changes, test.scope || JCal.instanceScope);
        
        // ignore sequence and dtstamp updates
        delete vcal.event.jcal.sequence;
        delete vcal.event.jcal.dtstamp;
        isDeeply (vcal.event.jcal, test.finish, test.name);

        // Check exceptions
        for (key in merge(vcal.exceptions, test.finishExceptions)) {
            var got = condGetProp(vcal.exceptions[key], 'jcal');
            var expected = test.finishExceptions[key];
            if (got == undefined && expected != undefined) {
                ok (false, 'Exception ' + key + ' is missing');
            } else if (got != undefined && expected == undefined) {
                ok (false, 'Found extra exception ' + key);
            } else {
                delete got.sequence;
                delete got.dtstamp;
                isDeeply (got, expected, test.name);
            }
        }
    } catch (e) {
        failTest (e, test.name);
    }
};

failTest = function (err, name)
{
    var msg = ['Failed Test: ', name, '\n'];
    var seen = {};
    for (var key in err) {
        if (!seen[key]) {
            msg.push (key, ': ', err[key], '\n');
            seen[key] = err[key];
        }
    }
    ok (false, msg.join(''));
};

forEach (EditTestCases, runEditTest);
//runEditTest(EditTestCases.slice(-1)[0]);
