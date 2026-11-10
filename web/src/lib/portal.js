// portal moves an element to the end of <body> while `on` is true and puts it
// back where it was otherwise – for a view that has to cover the whole page
// from inside a dialog (the dialog's blur would keep a fixed element inside it).
export function portal(node, on) {
  const home = document.createComment('');
  node.parentNode.insertBefore(home, node);
  const place = (v) => {
    if (v) document.body.appendChild(node);
    else if (home.parentNode && node.previousSibling !== home) home.parentNode.insertBefore(node, home.nextSibling);
  };
  place(on);
  return {
    update: place,
    destroy() {
      home.remove();
      if (node.parentNode === document.body) node.remove();
    },
  };
}
