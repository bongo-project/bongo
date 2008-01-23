Dragonfly.setLoginDisabled = function (disabled)
{
    var d = Dragonfly;
    var inputs = $('login-pane').getElementsByTagName('input');
    for (var i = inputs.length - 1; i >= 0; i--) {
        inputs[i].disabled = disabled;
    }
    
    var inputs = $('login-pane').getElementsByTagName('select');
    for (var i = inputs.length - 1; i >= 0; i--) {
        inputs[i].disabled = disabled;
    }
};

Dragonfly.setLoginMessage = function (msg)
{
    console.debug("Set from '" + $('login-message').innerHTML + "' to '" + msg + "'.");
    Element.setHTML ('login-message', msg);
};

Dragonfly.login = function (user)
{
    var d = Dragonfly;
    var p = d.Preferences;
    
    if (!user) {    
        user = d.getCurrentUser ();
    }
    if (!user) {
        user = d.getDefaultUser ();
    }
    if (!user) {
        d.showLoginPane();
        return;
    }

    function loginSucceeded () {
        if ($('login-default').checked) {
            d.setDefaultUser (user);
        }
        
        d.authToken = btoa (user + ':');
        
        return d.start();
    }

    console.debug ('trying to log in as ' + user);

    hideElement('login-button');
    showElement('status-indicator');
    d.userName = user;
    if (!d.authToken) {
        d.authToken = btoa (user + ':');
    }
    
    d.setLoginMessage ('Logging you in...');
    d.setLoginDisabled (true);
    
    p.load().addCallbacks (
        function (prefs) {
            return loginSucceeded ();
        },
        function (err) {
            if (!Element.visible ('login-pane')) {
                d.setLoginMessage ('&nbsp;');
                d.showLoginPane();
            } else if (err.req && (err.req.status == 401 || err.req.status == 403)) {
                // Only tell the user its their fault if we get a permission denied HTTP response.
                d.setLoginMessage (_('Incorrect username or password.'));
            } else if (err.req && err.req.status == 200) {
               d.setLoginMessage (_('The server backend seems to be fubared. It might be useful to check your Apache logs to see what the symptom is. It is usually one of two things:<ul><li>A Python exception, which can be determined by looking for a traceback, or;</li><li>You\'re missing mod_python. If this is the case, please nag so_solid_moo in #bongo.</li></ul>'));
               //logError ('backend was fubared and returned: ' + d.responseText);
           } else {
                d.setLoginMessage (_('Some error occured while logging in - check the logs.'));
                console.error ('login error: ' + d.reprError (err));
            }
            showElement('login-button');
            hideElement('status-indicator');
            d.setLoginDisabled (false);
            return err;
        });
};

Dragonfly.logout = function ()
{
    var d = Dragonfly;
    if (!d.stop()) {
        return;
    }
    if (d.userName) {
    	var storecookie = new d.Cookie (document, 'BongoAuthToken.' + escape(d.userName), -1, '/');
        storecookie.storeSimple();
        storecookie.remove();
    }
    var cookie = Dragonfly.getDefaultUserCookie ();
    cookie.$expires = -1;
    cookie.storeSimple();
    cookie.remove();
    if (Dragonfly.webkitVersion < 420) { // exercise a webkit quirk to force a reload
        location.hash = '';
        callLater (1, function () {location.hash = '';});
    } else { 
        location.hash = 'LoggedOut';
        location.reload();
    }
};

Dragonfly.translateLogin = function ()
{
    // Login screen
    Element.setHTML ('login-user-label', _('Name:'));
    Element.setHTML ('login-password-label', _('Password:'));
    Element.setHTML ('login-language-label', _('Language'));
    Element.setHTML ('login-default-label', _('Remember me'));
    $('login-button').value = _('Login');
};

Dragonfly.languageSuccess = function ()
{
    var d = Dragonfly;
    
    d.translateLogin ();
    d.setLoginDisabled (false);
    if (!d.langFailure)
    {
        d.setLoginMessage ('&nbsp;');
    }
};

Dragonfly.languageError = function (json)
{
    var d = Dragonfly;
    
    d.langFailure = true;
    
    if (json)
    {
        d.setLoginMessage ('Could not load translations. Check logs.<br />Using default language (English).');
        console.error ('error loading translations for "' + $('login-language').value + '": server returned status ' + json.status);
        console.error ('request content: ' + json.responseText);
        
        // Setup default form labels anyhoo.
        d.translateLogin();
    }
    else
    {
        d.setLoginMessage ('Could not load translations. Check logs.<br />Using default language (English).');
        console.error ('Hmm, JSON was undefined. :s');
    }
};

Dragonfly.languageChanged = function (evt)
{
    var d = Dragonfly;
    var g = Locale;

    d.setLoginMessage ('Loading language...');
    d.setLoginDisabled (true);
    g.setLocale('LC_MESSAGES', $('login-language').value);
    g.setSuffix('js');
    g.bindTextDomain('dragonfly', 'l10n', d.languageSuccess, d.languageError);
};

Dragonfly.observeLoginEvents = function ()
{
    Event.observe ('login-user', 'keypress',
                   (function (evt) {
                       if (evt.keyCode == Event.KEY_RETURN) {
                           $('login-password').focus();
                       }
                   }).bindAsEventListener (null));
    Event.observe ('login-password', 'keypress',
                   (function (evt) {
                       if (evt.keyCode == Event.KEY_RETURN) {
                           Dragonfly.submitLogin();
                       }
                   }).bindAsEventListener (null));
    Event.observe ('login-button', 'click',
                   Dragonfly.submitLogin.bindAsEventListener());
    Event.observe ('login-language', 'change',
                   Dragonfly.languageChanged.bindAsEventListener());

    Dragonfly.languageChanged();
};

Dragonfly.showLoginPane = function ()
{
    hideElement ('loading');
    hideElement ('content');
    showElement ('login-pane');
    showElement ('login-button');
    hideElement ('status-indicator');
    Dragonfly.setLoginDisabled (false);
    addElementClass (document.body, 'login');
    
    if (!console.type || console.type == "internal") {
        hideElement ('showLogs');
        hideElement ('makeDumpLink');
    }
    else if (console.type == "browser" && console.maxLogLevel == INFO) {
        // hide dump link if not debug build
        hideElement ('makeDumpLink');
    }

    if (location.hash == '#LoggedOut') {
        Dragonfly.setLoginMessage (Dragonfly.logoutMessage);
        Dragonfly.langFaliure = true;   // Stops us from removing the 'You have logged out' message when we load the lang.
        location.hash = (Dragonfly.isWebkit) ? '' : '#';
        $('login-user').focus();
    } else {
        var user = Dragonfly.getDefaultUser();
        if (user) {
            $('login-user').value = user;
            $('login-password').focus();
        } else {
            $('login-user').focus();
        }     
    }
};

Dragonfly.submitLogin = function ()
{
    var d = Dragonfly;

    var user = $('login-user').value;
    var pass = $('login-password').value;

    if (!user) {
        $('login-user').focus ();
        return;
    }

    d.setLoginMessage ('&nbsp;');

    d.authToken = btoa (user + ':' + pass); 
    d.login(user);
};

Dragonfly.setDefaultUser = function (user)
{
    var storecookie = new Dragonfly.Cookie (document, 'BongoAuthToken.' + escape(user), null, '/');
    var cookievalue = user;
    if (storecookie.loadSimple()) {
         // put store credentials in our cookie too.
        cookievalue += ',' + storecookie.$value;
    }
    var cookie = Dragonfly.getDefaultUserCookie ();
    cookie.$value = cookievalue;
    cookie.storeSimple();
};

Dragonfly.getDefaultUser = function ()
{
    var loc = new Dragonfly.Location (location.hash);
    if (loc && loc.user) {
        return loc.user;
    }
    var cookie = Dragonfly.getDefaultUserCookie ();
    if (!cookie.loadSimple())
    	return undefined;
    
    var cookiedata = cookie.$value.split(",");
    if (cookiedata[1]) {
        // there is a store cookie to restore
        var storecookie = new Dragonfly.Cookie (document, 'BongoAuthToken.' + cookiedata[0], null, '/');
        storecookie.$value = cookiedata[1];
        storecookie.storeSimple();
    }
    
    return cookiedata[0];
};

Dragonfly.getDefaultUserCookie = function ()
{
    return new Dragonfly.Cookie (document, 'defaultUser', 7 * 24, '/');
};

/* also deletes this cookie */
Dragonfly.getCurrentUser = function ()
{
    var d = Dragonfly;
    var user;

    var cookie = new d.Cookie (document, 'currentUser', -1);
    if (cookie.loadSimple()) {
        user = cookie.$value;
        delete cookie.$value;
        cookie.storeSimple();
    }
    console.debug ('got current user: ' + user);
    return user;
};

Dragonfly.setCurrentUser = function ()
{
    var d = Dragonfly;
    var cookie = new d.Cookie (document, 'currentUser', 1 / 120, '/');
    cookie.$value = d.userName;
    cookie.storeSimple();
};

Dragonfly.hasAuthCookie = function (username)
{
    var d = Dragonfly;
    return (new d.Cookie (document, 'BongoAuthToken.' + escape(username))).loadSimple();
};


