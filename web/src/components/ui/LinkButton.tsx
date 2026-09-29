import type { ReactNode } from "react";
import { Link, type LinkProps } from "react-router-dom";
import { buttonStyles } from "./Button";

interface Props extends Omit<LinkProps, "className"> {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  icon?: ReactNode;
  className?: string;
}

/** Enlace de navegación con la apariencia de un Button. */
export function LinkButton({ variant = "primary", size = "md", icon, className, children, ...rest }: Props) {
  return (
    <Link className={buttonStyles(variant, size, className)} {...rest}>
      {icon}
      {children}
    </Link>
  );
}

/** <a download> con la apariencia de un Button (para el scaffold.zip). */
export function DownloadLink({
  href,
  variant = "primary",
  size = "md",
  icon,
  children,
}: {
  href: string;
  variant?: "primary" | "secondary";
  size?: "sm" | "md";
  icon?: ReactNode;
  children: ReactNode;
}) {
  return (
    <a href={href} download className={buttonStyles(variant, size)}>
      {icon}
      {children}
    </a>
  );
}
