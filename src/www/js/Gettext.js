Dragonfly.Gettext = { };

Dragonfly.Gettext.setLocale = function (category, locale)
{
    var d = Dragonfly;
    var g = d.Gettext;

    if (g._def) {
        g._def.cancel();
    }

    if (g._locale == locale) {
        return succeed();
    }

    g._def = d.requestJSON ('GET', 'l10n/' + encodeURIComponent (locale) + '.js');
    return g._def.addCallbacks (
        function (jsob) {
            g._locale = locale;
            g._translations = jsob;
            return locale;
        },
        function (jsob) {
            delete g._def;
            /* leave old translations in place */
        });
};

Dragonfly.Gettext.gettext = function (msgid)
{
    var d = Dragonfly;
    var g = d.Gettext;

    // return (g._translations && g._translations[msgid]) || msgid;

    if (!g._translations) {
        logWarning ('No translations loaded.');
        return msgid;
    }

    if (!g._translations[msgid]) {
        logWarning ('No translation found for "'+msgid+'" in '+g._locale);
        return msgid;
    }

    return g._translations[msgid];
};

_ = Dragonfly.Gettext.gettext;

f_ = function (s)
{
    s = _(s);
    return Dragonfly.format.apply (this, arguments);
};
