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
