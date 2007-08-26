{
    abstract: {
        'name':         'Example Plugin',
        'version':      '1.2',
        'author':       'Alexander Hixon',
        'copyright':    '(c) 2007 Mediati Group'
    },
    
	install: function () {
	    // Create some non-standard element and shove it on the navbar
	    var obj = document.createElement('input');
	    obj.type = 'text';
	    Dragonfly.Plugins.Navbar.Append (obj);
	    
		return true;
	}
}
