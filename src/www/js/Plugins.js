/*  Dragonfly plugins demo stuff!  */
Dragonfly.Plugins = {};
Dragonfly.Plugins.Installed = [];

/*
    Loads the main plugin code off the server, then runs the install process on completion.
*/
Dragonfly.Plugins.load = function (plugin)
{
    log('Installing ', plugin, '...');
    this.pluginBase = plugin;
    var uri = '/plugins/' + this.pluginBase + '.js';
    var options = {
      method: 'get', onSuccess: this.loadPlugin.bind(this),
      onFailure: this.jsonFailure.bind(this)
    };
    
    logDebug('Making request for ' + uri);
    new Ajax.Request(uri, options);
};

/*
    Fired when a JSON request fails.
*/
Dragonfly.Plugins.jsonFailure = function (json)
{
    if (json.status == 404)
    {
        this.pluginFailure("Plugin wasn't found on server. Wrong name?");
    }
    else
    {
        this.pluginFailure("Server returned " + json.status + "; response text in log.");
        logError(json.responseText);
    }
};

/*
    Logs an error to the console, and notifys the user.
*/
Dragonfly.Plugins.pluginFailure = function (msg, err)
{
    var rmsg = "Failed to install plugin " + this.pluginBase;
    
    if (msg && err)
    {
        rmsg += ": " + msg + " - " + d.reprError (err);
    }
    else if (msg && !err)
    {
        rmsg += ": " + msg;
    }
    else if (err)
    {
        rmsg += ": " + d.reprError (err);
    }
    else
    {
        rmsg += ".";
    }
    
    logError(rmsg);
    Dragonfly.setLoginMessage(rmsg);
    Dragonfly.notifyError(rmsg, null, false);
};

/*
    Installs the plugin code fetched from loadInfo(jsob).
*/
Dragonfly.Plugins.loadPlugin = function (jsob)
{    
    var p = Dragonfly.Plugins;
   
    try
    {
        // Register the plugin.
        logDebug('Merging plugin code into Dragonfly...');
        eval('var plugin = ' + jsob.responseText);
        if (typeof plugin == "object") {
            this.plugin = plugin;
            p.Installed.push(this.plugin);
            
            logDebug('Registering plugin...');
            this.plugin.install();
            
            // Plugin author provided us with links to install.
            if (this.plugin.linkage)
            {
                if (this.plugin.linkage.navigation)
                {
                    if (!p.Navbar.Install(this.plugin.linkage.navigation))
                    {
                        logError('Failed to install navbar.');
                    }
                }
                
                if (this.plugin.linkage.sidebar)
                {
                    if (!p.Sidebar.Install(this.plugin.linkage.sidebar))
                    {
                        logError('Failed to install sidebar.');
                    }
                }
            }
            
            log('Plugin installed OK!');
            Dragonfly.notify('Plugin ' + this.pluginBase + ' successfully installed.');
        }
        else
        {
            this.pluginFailure("Plugin did not return an object.");
        }
    }
    catch (err)
    {
        this.pluginFailure(null, err);
    }
};

Dragonfly.Plugins.Navbar = {};

/*
    Appends a new object to the navbar.
*/
Dragonfly.Plugins.Navbar.Append = function (obj)
{
    var li = document.createElement('li');
    li.appendChild(obj);
    $('section-user').appendChild(li);
};

/*
    Creates a new navbar link.
    Returns the link element.
*/
Dragonfly.Plugins.Navbar.NewLink = function (href, title, text, onclick)
{
    var a = document.createElement('a');
    a.href = href;
    a.title = title;
    a.innerHTML = text;
    if (onclick)
    {
        a.onclick = onclick;
        a.ignoreClick = true;
    }
    
    return a;
};

/*
    Uses supplied JSON data from a plugin to install navbar links into Dragonfly.
    Returns true if succeeded, false otherwise.
*/
Dragonfly.Plugins.Navbar.Install = function (json)
{
    var n = Dragonfly.Plugins.Navbar;
    
    try
    {
        json.each( function(item) {
            n.Append(n.NewLink(item.href, item.title, item.text, item.onclick));
        });
    }
    catch (err)
    {
        this.pluginFailure('Failed to install navbar', err);
        return false;
    }
    
    return true;
};

Dragonfly.Plugins.Sidebar = {};

/*
    Creates a new parent sidebar tab.
    Returns the new tab element.
*/
Dragonfly.Plugins.Sidebar.NewTab = function (href, title, text, onclick)
{
    var div = document.createElement('div');
    div.className = 'tab';
    
    var linkage = document.createElement('a');
    linkage.href = href;
    linkage.title = title;
    if (onclick)
    {
        linkage.onclick = onclick;
        a.ignoreClick = true;
    }
    
    var label = document.createElement('strong');
    label.innerHTML = text;
    
    linkage.appendChild(label);
    div.appendChild(linkage);
    
    return div;
};

Dragonfly.Plugins.Sidebar.Append = function (obj)
{
    // TODO: Support for 'tiny-tabs'.
    
    var div = document.createElement('div');
    div.class = 'tab-spacer';
    
    // Append spacer, then new tab.
    $('sidebar-tabs-large').appendChild(div);
    $('sidebar-tabs-large').appendChild(obj);
}

Dragonfly.Plugins.Sidebar.Install = function (json)
{
    var s = Dragonfly.Plugins.Sidebar;
    
    // TODO: Provide support for child tabs and 'tiny-tabs'.
    try
    {
        json.each( function(item) {
            s.Append(s.NewTab(item.href, item.title, item.text, item.onclick));
        });
    }
    catch (err)
    {
        this.pluginFailure('Failed to install sidebar', err);
        return false;
    }
    
    return true;
};
