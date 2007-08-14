/*
    eArea - a simple web-based text editor
    Copyright (C) 2006  Oliver Moran

    This library is free software; you can redistribute it and/or
    modify it under the terms of the GNU Lesser General Public
    License as published by the Free Software Foundation; either
    version 2.1 of the License, or (at your option) any later version.

    This library is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
    Lesser General Public License for more details.

    You should have received a copy of the GNU Lesser General Public
    License along with this library; if not, write to the Free Software
    Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA
*/

function insertEditableArea(editableAreaName, editableAreaWidth, editableAreaHeight, editableAreaLayout, editableAreaStyle) {
	var boldButton = '<input type="image" src="/js/eArea/icons/format-text-bold.png" value="Bold" alt="Bold" name="' + editableAreaName + '" id="bold" class="icon" style="width:24px;height:24px;">';
	var italicButton = '<input type="image" src="/js/eArea/icons/format-text-italic.png" value="Italic" alt="Italic" name="' + editableAreaName + '" id="italic" class="icon" style="width:24px;height:24px;">';
	var underlinedButton = '<input type="image" src="/js/eArea/icons/format-text-underline.png" value="Underline" alt="Underline" name="' + editableAreaName + '" id="underline" class="icon" style="width:24px;height:24px;">';
	var alignLeftButton = '<input type="image" src="/js/eArea/icons/format-justify-left.png" value="Align Left" alt="Align Left" name="' + editableAreaName + '" id="justifyleft" class="icon" style="width:24px;height:24px;">';
	var justifyButton = '<input type="image" src="/js/eArea/icons/format-justify-fill.png" value="Justify" alt="Justify" name="' + editableAreaName + '" id="justifyfull" class="icon" style="width:24px;height:24px;">';
	var alignCenterButton = '<input type="image" src="/js/eArea/icons/format-justify-center.png" value="Align Center" alt="Align Center" name="' + editableAreaName + '" id="justifycenter" class="icon" style="width:24px;height:24px;">';
	var alignRightButton = '<input type="image" src="/js/eArea/icons/format-justify-right.png" value="Align Right" alt="Align Right" name="' + editableAreaName + '" id="justifyright" class="icon" style="width:24px;height:24px;">';
	var undoButton = '<input type="image" src="/js/eArea/icons/edit-undo.png" value="Undo" alt="Undo" name="' + editableAreaName + '" id="undo" class="icon" style="width:24px;height:24px;">';
	var redoButton = '<input type="image" src="/js/eArea/icons/edit-redo.png" value="Redo" alt="Redo" name="' + editableAreaName + '" id="redo" class="icon" style="width:24px;height:24px;">';
	var selectAllButton = '<input type="image" src="/js/eArea/icons/edit-select-all.png" value="Select all" alt="Select all" name="' + editableAreaName + '" id="selectall" class="icon" style="width:24px;height:24px;">';
	var insertImageButton = '<input type="image" src="/js/eArea/icons/insert-image.png" value="Insert image" alt="Insert image" name="' + editableAreaName + '" id="insertimage" class="icon" style="width:24px;height:24px;">';
	var insertLinkButton = '<input type="image" src="/js/eArea/icons/insert-link.png" value="Insert link" alt="Insert link" name="' + editableAreaName + '" id="insertlink" class="icon" style="width:24px;height:24px;">';
	var switchModeButton = '<input type="image" src="/js/eArea/icons/switch-mode.png" value="Switch mode" alt="Switch mode" name="' + editableAreaName + '" id="switchmode" class="icon" style="width:24px;height:24px;">';
	var editableArea = '<iframe src="/js/eArea/blank.html?style=' + editableAreaStyle + '" id="' + editableAreaName + '" \n style="width:' + editableAreaWidth + '; min-height:' + editableAreaHeight + '; border-style:solid; border: 1px solid #DDDDDD;" frameborder="0px"></iframe>'

	var editableAreaHTML = editableAreaLayout;
	editableAreaHTML = editableAreaHTML.replace("[bold]", boldButton);
	editableAreaHTML = editableAreaHTML.replace("[italic]", italicButton);
	editableAreaHTML = editableAreaHTML.replace("[underlined]", underlinedButton);
	editableAreaHTML = editableAreaHTML.replace("[align-left]", alignLeftButton);
	editableAreaHTML = editableAreaHTML.replace("[justify]", justifyButton);
	editableAreaHTML = editableAreaHTML.replace("[align-center]", alignCenterButton);
	editableAreaHTML = editableAreaHTML.replace("[align-right]", alignRightButton);
	editableAreaHTML = editableAreaHTML.replace("[undo]", undoButton);
	editableAreaHTML = editableAreaHTML.replace("[redo]", redoButton);
	/*editableAreaHTML = editableAreaHTML.replace("[paste]", pasteButton);      NOT SUPPORTED ON MOZILLA-BASED
	                                                                            BROWSERS FOR SECURITY REASONS!
    */
    editableAreaHTML = editableAreaHTML.replace("[select-all]", selectAllButton);
    editableAreaHTML = editableAreaHTML.replace("[insert-image]", insertImageButton);
    editableAreaHTML = editableAreaHTML.replace("[insert-link]", insertLinkButton);
    editableAreaHTML = editableAreaHTML.replace("[switch-mode]", switchModeButton);
	
	editableAreaHTML = editableAreaHTML.replace(/sep/g, '<span class="split"></span>');
	editableAreaHTML = editableAreaHTML.replace("[edit-area]", editableArea);
	
	retHTML = '';
	
	if (document.designMode) {
		retHTML = editableAreaHTML;
		//ititButtons(editableAreaName);
	} else {
		// create a normal <textarea> if document.designMode does not exist
		retHTML = '<textarea id="' + editableAreaName + '" style="width:' + editableAreaWidth + '; height:' + editableAreaHeight + '; ' + editableAreaStyle + '"></textarea>';
	}
	
	return retHTML;
}

// TODO!
function stripHTML(d)
{
    /*
    foreach ($tags as $tag){
        while(preg_match('/<'.$tag.'(|\W[^>]*)>(.*)<\/'. $tag .'>/iusU', $text, $found)){
            $text = str_replace($found[0],$found[2],$text);
        }
    }

    return preg_replace('/(<('.join('|',$tags).')(|\W.*)\/>)/iusU', '', $text);
    */
    
    d = d.replace(/<a[^href]+href=\"([^\"]*)\"[^>]*>/ig,"$1");      // Linkies
    d = d.replace("<b>", "*");                                      // Vold (that's bold with an accent ;)
    d = d.replace("<strong>", "*");
    //d = d.replace("<br />"
    //d = d.replace(/(<([^>]+)>)/ig, "");                             // Remove the other tags we can't handle.
    
    return d;
}

function editableAreaContents(editableAreaName) {
	if (document.designMode) {
		// Explorer reformats HTML during document.write() removing quotes on element ID names
		// so we need to address Explorer elements as window[elementID]
		if (window[editableAreaName]) return window[editableAreaName].document.body.innerHTML;
		return document.getElementById(editableAreaName).contentWindow.document.body.innerHTML;
	} else {
		// return the value from the <textarea> if document.designMode does not exist
		return document.getElementById(editableAreaName).value;
	}
}

// Enable/disable HTML buttons
// TODO!!!
function setHTMLbuttons(value)
{
    var obj = $('toolbar').childNodes;
    for (var i=0; i < obj.length; i++)
    {
        var btn = obj[i];
        if (btn.attributes.getNamedItem('isHtml').value == "true")
        {
            btn.style.class = "icon selected";
        }
    }
}

function setEditableAreaContents(editableAreaName, value)
{
    if (document.designMode) {
		// Explorer reformats HTML during document.write() removing quotes on element ID names
		// so we need to address Explorer elements as window[elementID]
		if (window[editableAreaName])
		{
		    window[editableAreaName].document.body.innerHTML = value;
		}
		else
		{
		    document.getElementById(editableAreaName).contentWindow.document.body.innerHTML = value;
		}
	} else {
		// return the value from the <textarea> if document.designMode does not exist
		document.getElementById(editableAreaName).value = value;
	}
}

function ititButtons(editableAreaName) {
	var kids = document.getElementsByTagName('input');

	for (var i=0; i < kids.length; i++) {
		if (kids[i].name == editableAreaName) {
			kids[i].onmouseover = buttonMouseOver;
			kids[i].onmouseout = buttonMouseOut;
			kids[i].onmouseup = buttonMouseUp;
			kids[i].onmousedown = buttonMouseDown;
			kids[i].onclick = buttonOnClick;
		}
	}
}

function makeWrappedTextarea(editableAreaName)
{
    // Grab the contents to put in the normal textarea.
    var contents = editableAreaContents(editableAreaName);
    var style = "'DejaVu Sans Mono', 'Bitstream Vera Sans Mono', 'Monaco', 'Courier', monospace";
    
    // Hide the HTML buttons.
    // TODO!
    
    // Set the style for the inner iframe thing (only on browsers that support designMode, since textarea is already ok)
    if (window[editableAreaName])
    {
        window[editableAreaName].document.body.style.fontFamily = style;
    }
    else
    {
		document.getElementById(editableAreaName).contentWindow.document.body.style.fontFamily = style;
    }
    
    // Now, loop and fix up wrapping.
    setEditableAreaContents(editableAreaName, fixWrap(contents, 75));
    
    // Setup events to keep wrapping in order.
    
}

function fixWrap(msg, lineLength)
{
    // Split it into words!
    msg = msg.split(' ');
    var currentLine = 0;
    
    var rmsg = new Array();
    
    for (i=0;i<msg.length;i++)
    {
        if (msg[i].length >= lineLength)
        {
            // We've got a B-I-G word; split it at lineLength.
            var offset = 0;
            var startLine = 0;
            var extra = 0;
            for (x=0;x<msg[i].length;x++)
            {
                if (offset == lineLength)
                {
                    // Split it here!
                    rmsg.push('<br>' + msg[i].substring(startLine, startLine + offset));
                    startLine += offset;
                    offset = 0;
                    extra += 2;
                }
                else
                {
                    offset++;
                }
            }
            
            currentLine = msg[i].length + extra;
        }
        else
        {
            // Check if adding the new word makes us go over the limit.
            if ((currentLine + msg[i].length) >= lineLength)
            {
                // Uh oh, bad.
                // Split time!
                
                currentLine = msg[i].length + 1;
                //currentLine = 0;
                rmsg.push("<br>" + msg[i]);
            }
            else
            {
                // Word is less than lineLength, handle it normally.
                rmsg.push(msg[i]);
                currentLine += msg[i].length + 1;
            }
        }
    }
    
    return rmsg.join(' ');
}

function buttonMouseOver() {
	// events for mouseOver on buttons
	// e.g. this.style.xxx = xxx
}

function buttonMouseOut() {
	// events for mouseOut on buttons
	// e.g. this.style.xxx = xxx
}


function buttonMouseUp() {
	// events for mouseUp on buttons
	// e.g. this.style.xxx = xxx
}

function buttonMouseDown(e) {
	// events for mouseDown on buttons
	// e.g. this.style.xxx = xxx

	// prevent default event (i.e. don't remove focus from text area)
	var evt = e ? e : window.event; 

	if (evt.returnValue) {
		evt.returnValue = false;
	} else if (evt.preventDefault) {
		evt.preventDefault( );
	} else {
		return false;
	}
}

function runCmd(editableAreaName, id, ui, value)
{
    // Explorer reformats HTML during document.write() removing quotes on element ID names
	// so we need to address Explorer elements as window[elementID]
	if (window[editableAreaName]) { window[editableAreaName].document.execCommand(id, ui, value); }
	else { document.getElementById(editableAreaName).contentWindow.document.execCommand(id, ui, value); }
}

function buttonOnClick() {
    if (this.id == "insertimage")
    {
        var path = prompt('Enter image url:','http://');
        if (path && path.length > 10)
        {
            runCmd(this.name, this.id, null, path);
        }
    }
    else if (this.id == "insertlink")
    {
        var path = prompt('Enter URL:','http://');
        var text = prompt('Link text:','New link');
        if (path && path.length > 10 && text)
        {
            $(this.name).insertAdjacentHTML('beforeEnd', '<a href="' + path + '">' + text + '</a>');
            //runCmd(this.name, this.id, null, path);
        }
    }
    else if (this.id == "switchmode")
    {
        // Dragonfly must be included here; otherwise you'll get script errors!
        if (Dragonfly)
        {
            var m = Dragonfly.Mail;
            if (m.isHtml)
            {
                // Grab the contents to put in the normal textarea.
                var style = "'DejaVu Sans Mono', 'Bitstream Vera Sans Mono', 'Monaco', 'Courier', monospace";
                
                // Disable HTML-styling buttons / change onclick events.
                
                // Set the style for the inner iframe thing (only on browsers that support designMode, since textarea is already ok)
                if (window[this.name])
                {
                    window[this.name].document.body.style.fontFamily = style;
                    window[this.name].document.body.style.width = "47em";
                }
                else
                {
            		document.getElementById(this.name).contentWindow.document.body.style.fontFamily = style;
            		document.getElementById(this.name).contentWindow.document.body.style.width = "47em";
                }
                
                setEditableAreaContents(this.name, stripHTML(editableAreaContents(this.name)));
                
                m.isHtml = false;
            }
            else
            {
                var style = m.defaultComposerStyle;
                
                // Re-enable HTML-styling buttons / change onclick events.
                
                // Set the style for the inner iframe thing (only on browsers that support designMode, since textarea is already ok)
                if (window[this.name])
                {
                    window[this.name].document.body.setAttribute('style', style);
                }
                else
                {
            		document.getElementById(this.name).contentWindow.document.body.setAttribute('style', style);
                }
                
                m.isHtml = true;
            }
            
            //alert('isHtml? ' + m.isHtml);
        }
        else
        {
            alert('Dragonfly not loaded - can\'t switch composition modes.');
        }
    }
    else
    {
        runCmd(this.name, this.id, false, null);
    }
}
