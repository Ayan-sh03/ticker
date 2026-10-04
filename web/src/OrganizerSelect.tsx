/* Hallmark · component: select · genre: modern-minimal · theme: existing Coral tokens
 * pre-emit critique: P4 H4 E4 S4 R5 V4 */
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';

type Option = { value: string; label: string };
type Props = {
  id: string;
  label: string;
  value: string;
  options: readonly Option[];
  onChange: (value: string) => void;
  disabled?: boolean;
};

// Focus stays on the combobox; the listbox is in the top layer so it cannot be
// clipped by the form. Native popover light-dismiss also handles touch devices.
export function OrganizerSelect({ id, label, value, options, onChange, disabled = false }: Props) {
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const search = useRef({ text: '', at: 0 });
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const selected = options.findIndex(option => option.value === value);
  const menuID = `${id}-options`;
  const optionID = (index: number) => `${id}-option-${index}`;

  function close() {
    if (menu.current?.matches(':popover-open')) menu.current.hidePopover();
    setOpen(false);
    search.current = { text: '', at: 0 };
  }

  function show(index = Math.max(0, selected)) {
    const button = trigger.current, list = menu.current;
    if (!button || !list || button.matches(':disabled') || !options.length) return;
    const rect = button.getBoundingClientRect();
    const height = Math.min(options.length * 48 + 16, 288);
    const below = window.innerHeight - rect.bottom - 16;
    const above = rect.top - 16;
    const flip = below < height && above > below;
    const available = Math.max(48, flip ? above : below);
    const width = Math.min(rect.width, window.innerWidth - 16);
    list.style.width = `${width}px`;
    list.style.left = `${Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))}px`;
    list.style.top = `${flip ? Math.max(8, rect.top - Math.min(height, available) - 8) : rect.bottom + 8}px`;
    list.style.maxHeight = `${Math.min(288, available)}px`;
    setActive(index);
    if (!list.matches(':popover-open')) list.showPopover();
    setOpen(true);
  }

  function choose(index: number) {
    if (trigger.current?.matches(':disabled') || !options[index]) return;
    close();
    trigger.current?.focus();
    if (options[index].value !== value) onChange(options[index].value);
  }

  useEffect(() => {
    const list = menu.current;
    if (!list) return;
    const sync = () => {
      const visible = list.matches(':popover-open');
      setOpen(visible);
      if (!visible) search.current = { text: '', at: 0 };
    };
    list.addEventListener('toggle', sync);
    return () => list.removeEventListener('toggle', sync);
  }, []);

  useEffect(() => { if (disabled) close(); }, [disabled]);
  useEffect(() => {
    if (!open) return;
    const list = menu.current, option = document.getElementById(optionID(active));
    if (!list || !option) return;
    // Only scroll the list. scrollIntoView also scrolls the document, which
    // would dismiss this fixed popover through the page-scroll handler below.
    const listTop = list.getBoundingClientRect().top + list.clientTop;
    const optionRect = option.getBoundingClientRect();
    if (optionRect.top < listTop) list.scrollTop -= listTop - optionRect.top;
    else if (optionRect.bottom > listTop + list.clientHeight) list.scrollTop += optionRect.bottom - listTop - list.clientHeight;
  }, [active, open, id]);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: Event) => {
      if (event.target instanceof Node && menu.current?.contains(event.target)) return;
      close();
    };
    window.addEventListener('resize', dismiss);
    window.addEventListener('scroll', dismiss, true);
    return () => {
      window.removeEventListener('resize', dismiss);
      window.removeEventListener('scroll', dismiss, true);
    };
  }, [open]);

  function keyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.altKey || event.ctrlKey || event.metaKey || !options.length) return;
    if (event.key === 'Tab') { close(); return; }
    if (event.key === 'Escape') { if (open) { event.preventDefault(); close(); } return; }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const direction = event.key === 'ArrowDown' ? 1 : -1;
      show(open ? (active + direction + options.length) % options.length : Math.max(0, selected));
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault(); show(event.key === 'Home' ? 0 : options.length - 1);
    } else if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault(); if (open) choose(active); else show();
    } else if (event.key.length === 1) {
      event.preventDefault();
      const now = Date.now();
      let text = `${now - search.current.at < 700 ? search.current.text : ''}${event.key.toLocaleLowerCase()}`;
      if ([...text].every(character => character === text[0])) text = text[0];
      search.current = { text, at: now };
      const start = open ? active : selected;
      for (let step = 1; step <= options.length; step++) {
        const index = (start + step + options.length) % options.length;
        if (options[index].label.toLocaleLowerCase().startsWith(text)) { show(index); break; }
      }
    }
  }

  return <div className="organizer-select">
    <button id={id} ref={trigger} type="button" className="organizer-select-trigger" role="combobox"
      aria-label={label} aria-haspopup="listbox" aria-expanded={open} aria-controls={menuID}
      aria-activedescendant={open ? optionID(active) : undefined} disabled={disabled}
      onKeyDown={keyDown} onClick={() => open ? close() : show()}
      onBlur={() => close()}>
      <span className="organizer-select-value">{options[selected]?.label ?? 'Choose an option'}</span>
      <svg className="organizer-select-chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
    </button>
    <div id={menuID} ref={menu} popover="auto" role="listbox" aria-label={label} className="organizer-select-menu">
      {options.map((option, index) => <div id={optionID(index)} key={option.value} role="option"
        aria-selected={option.value === value} data-active={index === active ? 'true' : undefined}
        className="organizer-select-option" onPointerDown={event => event.preventDefault()}
        onPointerMove={event => { if (event.pointerType === 'mouse') setActive(index); }} onClick={() => choose(index)}>
        <span>{option.label}</span><span className="organizer-select-check" aria-hidden="true">{option.value === value ? '✓' : ''}</span>
      </div>)}
    </div>
  </div>;
}
