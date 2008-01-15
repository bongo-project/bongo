var ConsoleUtils = {
    runQuery: function(elem) {
        var box = document.getElementById(elem);
        try
        {
            var res = eval(box.value);
            if (res)
            {
                console.log(res);
            }
        }
        catch (err)
        {
            console.error(err.message || err);
        }
        finally
        {
            box.value = "";
        }
    },
    
    errorHandler: function(msg, url, line) {
        if (console.type == "browser")
        {
            console.pageError(msg, url, line);
        }
        else
        {
            console.error(msg, " in ", url, " on line ", line);
        }
    },
    
    showHideConsole: function() {
        console._toggle(console.currentDiv + "box");
    }
};


if (!console)
{
    var DEBUG = 3;
    var INFO = 2;
    var WARNING = 1;
    var ERROR = 0;
    
    // setup error handling.
    onerror = ConsoleUtils.errorHandler;

    var console = {
                    currentDiv: "log",
                    previousDivs: [ ],
                    
                    /* set this to INFO when making a release. */
                    maxLogLevel: DEBUG,
                    
                    type: "browser",
                    internalFatString: "",
                    textIndent: "",
    
                    _logEvent: function(msg, className, prefix, callerprop, errStr) {
                        var logbox = document.getElementById(this.currentDiv);
                        var div = window.parent.document.createElement('div');
                        var datetime = new Date();
                        div.className = className;
                        
                        var ext = "";
                        if (callerprop)
                        {
                            div.innerHTML = "<span class='floatright'>(called from " + callerprop.name + ")</span>";
                            ext = "(called from " + callerprop.name + ")";
                        }
                        else if (errStr)
                        {
                            div.innerHTML = "<span class='floatright'>" + errStr + "</span>";
                            ext = "(error at " + errStr + ")";
                        }
                        else
                        {
                            div.innerHTML = "";
                        }
                        div.innerHTML += "<strong>" + prefix + "</strong> " + datetime.toLocaleString() + " - " + msg;
                        
                        logbox.appendChild(div);
                        logbox.scrollTop = logbox.scrollHeight;
                        
                        this.internalFatString += this.textIndent + prefix + " " + datetime.toLocaleString() + " - " + msg + (ext ? " " + ext + "\n" : "\n");
                    },
                    
                    pageError: function(msg, file, line) {
                        this._logEvent(msg, "error", "[ EROR ]", null, file + ":" + line);
                    },
                    
                    _createLink: function(href, title, text) {
                        var a = document.createElement('a');
                        a.href = href;
                        a.title = title;
                        a.innerHTML = text;
                        
                        return a;
                    },
                    
                    _toggle: function(id) {
                        if (document.getElementById(id).style.display == 'none')
                        {
                            document.getElementById(id).style.display = 'block';
                        }
                        else
                        {
                            document.getElementById(id).style.display = 'none';
                        }
                    },
                    
                    log: function() {
                        if (this.maxLogLevel >= INFO)
                        {
                            var msg = "";
                            for (var x = 0; x < arguments.length; x++)
                            {
                                msg += arguments[x].toString() || arguments[x] + " ";
                            }
                            
                            this._logEvent(msg, "info",  "[ INFO ]", this.log.caller);
                        }
                    },
                    
                    info: function() {
                        if (this.maxLogLevel >= INFO)
                        {
                            var msg = "";
                            for (var x = 0; x < arguments.length; x++)
                            {
                                msg += arguments[x].toString() || arguments[x] + " ";
                            }
                        
                            this._logEvent(msg, "info",  "[ INFO ]", this.info.caller);
                        }
                    },
                    
                    debug: function() {
                        if (this.maxLogLevel >= DEBUG)
                        {
                            var msg = "";
                            for (var x = 0; x < arguments.length; x++)
                            {
                                msg += arguments[x].toString() || arguments[x] + " ";
                            }
                        
                            this._logEvent(msg, "debug", "[ DBUG ]", this.debug.caller);
                        }
                    },
                    
                    warn: function() {
                        if (this.maxLogLevel >= WARNING)
                        {
                            var msg = "";
                            for (var x = 0; x < arguments.length; x++)
                            {
                                msg += arguments[x].toString() || arguments[x] + " ";
                            }
                        
                            this._logEvent(msg, "warn",  "[ WARN ]", this.warn.caller);
                        }
                    },
                    
                    error: function() {
                        if (this.maxLogLevel >= ERROR)
                        {
                            var msg = "";
                            for (var x = 0; x < arguments.length; x++)
                            {
                                msg += arguments[x].toString() || arguments[x] + " ";
                            }
                        
                            this._logEvent(msg, "error", "[ EROR ]", this.error.caller);
                        }
                    },
                    
                    group: function(gname) {
                        var linky = this._createLink("javascript:console._toggle('" + gname + "');", "Hide/unhide this group", "");
                        var spany = window.parent.document.createElement("div");
                        spany.className = "titlespan";
                        spany.innerHTML = gname;
                        linky.appendChild(spany);
                    
                        var div = window.parent.document.createElement('div');
                        var divbox = window.parent.document.createElement('div');
                        divbox.className = "groupbox";
                        div.className = "group";
                        div.id = gname;
                        div.style.display = "block";
                        
                        var logbox = document.getElementById(this.currentDiv);
                        divbox.appendChild(linky);
                        divbox.appendChild(div);
                        logbox.appendChild(divbox);
                        logbox.scrollTop = logbox.scrollHeight;
                        
                        this.previousDivs.push(this.currentDiv);
                        this.currentDiv = gname;
                        
                        this.textIndent += "  ";
                        
                        var maxlen = 80;    // chars of total length allowed
                        var minlen = 7;     // min number of chars can draw
                        var drawlen = maxlen - (minlen + this.textIndent.length);
                        drawlen = drawlen - gname.length + 2;   // 2 comes from magic; don't ask, it works.
                        
                        if (drawlen >= minlen)
                        {
                            // only draw if it'll fit
                            var mydrawing = "==[ " + gname + " ]=";
                            while (drawlen > 0)
                            {
                                mydrawing += "=";
                                drawlen--;
                            }
                            
                            this.internalFatString += mydrawing + "\n";
                        }
                    },
                    
                    groupEnd: function() {
                        this.currentDiv = this.previousDivs.pop();
                        this.textIndent = this.textIndent.substring(2);
                        
                        this.internalFatString += "\n\n";
                    },
                    
                    assert: function(bool, msg) {
                        // TODO: Don't just have one arg for msg
                        if (!bool)
                        {
                            console.error("Assertion failed: ", msg);
                        }
                    },
                    
                    create_xhr: function () {
                        try { return new ActiveXObject('MSXML3.XMLHTTP'); } catch(e) {}
                        try { return new ActiveXObject('MSXML2.XMLHTTP.3.0'); } catch(e) {}
                        try { return new ActiveXObject('Msxml2.XMLHTTP'); } catch(e) {}
                        try { return new ActiveXObject('Microsoft.XMLHTTP'); } catch(e) {}
                        try { return new XMLHttpRequest(); } catch(e) {}
                        throw new Error('Could not find XMLHttpRequest or an alternative.');
                    },
                    
                    sendLog: function(showAlert) {
                        xhr = this.create_xhr();
                        xhr.onreadystatechange = function (showAlert) {
                            try {
                                if (xhr.status != 200) {
                                    console.error('xhr failed (status not 200, was ' + xhr.status + ' instead)');
                                    if (xhr.reponseText) {
                                        console.debug("Response text: " + xhr.reponseText);
                                    }
                                return false;
                                }
                            }
                            catch(e) {
                                return false;
                            }
                            
                            // We have some new data
                            if (xhr.readyState == 4) {
                                if (xhr.responseText == "FAIL")
                                {
                                    console.error("Mistructed log data sent in request.");
                                }
                                else if (xhr.responseText == "BAD-RESP")
                                {
                                    console.error("Pastebin provider returned bad data.");
                                }
                                else
                                {
                                    console.info("Log URL: <a href='http://slexy.org/view/" + xhr.responseText + "'>http://slexy.org/view/" + xhr.responseText + "</a>");
                                }
                                
                                if (showAlert)
                                {
                                    prompt("A dump of your current log is available from here:", "http://slexy.org/view/" + xhr.responseText);
                                }
                            }
                        };
                        
                        xhr.open('POST', 'user/sendlog', true);
                        xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');
                        xhr.send("desc=Log+generated+on+" + new Date().toLocaleString() + "+from+" + window.location + "&contents=" + this.internalFatString);
                        
                        console.debug("Sent log data, waiting for response...");
                    }
                };
    
    console.info("Using in-page log - browser does not support 'console' object.");
}
else
{
    console.info("Hiding in-page log - using browser log infrastructure.");
    //document.getElementById("logbox").style.display = "none";
    //document.getElementById("jsprompt").style.display = "none";
    console.sendLog = function() { alert("Browser log does not support this method."); };
    
    console.type = "internal";
}
