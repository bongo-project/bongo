var Cookie = {
  set: function(name, value, daysToExpire) {
    var expire = '';
    if (daysToExpire != undefined) {
      var d = new Date();
      d.setTime(d.getTime() + (86400000 * parseFloat(daysToExpire)));
      expire = '; expires=' + d.toGMTString();
    }
    return (document.cookie = escape(name) + '=' + escape(value || '') + expire + '; path=/');
  },
  get: function(name) {
    var cookie = document.cookie.match(new RegExp('(^|;)\\s*' + escape(name) + '=([^;\\s]*)'));
    return (cookie ? unescape(cookie[2]) : null);
  },
  erase: function(name) {
    var cookie = Cookie.get(name) || true;
    Cookie.set(name, '', -1);
    return cookie;
  },
  accept: function() {
    if (typeof navigator.cookieEnabled == 'boolean') {
      return navigator.cookieEnabled;
    }
    Cookie.set('_test', '1');
    return (Cookie.erase('_test') === '1');
  }
};

var awesomeo = 0;

if (Cookie.get('_sidebar') == '1')
{
    awesomeo = 1;
    // Hide the sidebar at start.
    $('sidebar').hide();
    $('page').style.marginLeft = "0px";
    //$('sidebartoggle').style.color = "#eeeeec;"
    $('sidebartoggle').style.top = "0px";
}

function toggleSidebar()
{
    Effect.toggle('sidebar', 'blind');
    if (awesomeo == 0)
    {
        Cookie.set('_sidebar', '1');
        awesomeo = 1;
        $('page').style.marginLeft = "0px";
        //$('sidebartoggle').style.color = "#eeeeec;"
        $('sidebartoggle').style.top = "0px";
    }
    else
    {
        awesomeo = 0;
        Cookie.erase('_sidebar');
        $('page').style.marginLeft = "175px";
        $('sidebartoggle').style.top = "-50px";
        //$('sidebartoggle').style.color = "#2e3436";
    }
}

function addToList(div)
{
    var d = prompt("Value:", "");
    if (d)
    {
        var e = document.createElement('option');
        e.text = d;
        try
        {
            // W3C
            $(div).add(e, null);
        }
        catch(ex)
        {
            //alert(ex);
            // IE
            $(div).add(e);
        }
        
        $(div).selectedIndex = $(div).options.length - 1;
        
        Effect.Appear(div + '-removebtn');
        new Effect.Highlight(div);
    }
}

function removeFromList(div)
{
    var start = $(div).selectedIndex;
    $(div).remove(start);
    if ($(div).selectedIndex != start)
    {
        //new Effect.Highlight(div);
    }
    
    // Check if there's anymore items left.
    if ($(div).options.length == 0)
    {
        // Hide the remove button for zaaroo ooptoons.
        Effect.SwitchOff(div + '-removebtn');
    }
    else
    {
        // Update newly selected.
        $(div).selectedIndex = start - 1;
    }
}
