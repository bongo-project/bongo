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

    logDebug ('trying to log in as ' + user);

    hideElement('login-button');
    showElement('status-indicator');
    d.userName = user;
    if (!d.authToken) {
        d.authToken = btoa (user + ':');
    }
    
    d.setLoginMessage (_('loggingInMessage'));
    d.setLoginDisabled (true);
    
    p.load().addCallbacks (
        function (prefs) {
            return loginSucceeded ();
        },
        function (err) {
            if (!Element.visible ('login-pane')) {
                d.setLoginMessage ('&nbsp;');
                d.showLoginPane();
            } else if (err.req) {
                d.setLoginMessage (_('wrongCredentials'));
            } else {
                d.setLoginMessage (_('genericLoginError'));
                logError ('login error: ' + d.reprError (err));
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
    	var storecookie = new d.Cookie (document, 'BongoAuthToken.' + d.userName, -1, '/');
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
    Element.setHTML ('login-user-label', _('loginNameLabel'));
    Element.setHTML ('login-password-label', _('loginPasswordLabel'));
    Element.setHTML ('login-language-label', _('loginLanguageLabel'));
    Element.setHTML ('login-default-label', _('loginRememberMeLabel'));
    $('login-button').value = _('loginLoginLabel');
    Dragonfly.logoutMessage = _('loggedOutMessage');    
};

Dragonfly.languageChanged = function (evt)
{
    var d = Dragonfly;
    var g = d.Gettext;

    //d.setLoginMessage ('Loading language...');
    d.setLoginDisabled (true);
    g.setLocale (null, $('login-language').value).addCallbacks (
        function (res) {
            //d.setLoginMessage ('&nbsp;');
            d.translateLogin ();
            d.setLoginDisabled (false);
            return res;
        },
        function (err) {
            d.setLoginMessage ('Could not load translations.');
            logError ('error loading translations for' + $('login-language').value + ':', 
                      reprError (err));
            $('login-langage').value = g._locale || 'en';
            return err;
        });
        
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

    if (location.hash == '#LoggedOut') {
        Dragonfly.setLoginMessage (Dragonfly.logoutMessage);
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
    var storecookie = new Dragonfly.Cookie (document, 'BongoAuthToken.' + user, null, '/');
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
    logDebug ('got current user: ' + user);
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
    return (new d.Cookie (document, 'BongoAuthToken.' + username)).loadSimple();
};


